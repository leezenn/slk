package cmd

import (
	"bufio"
	"fmt"
	"runtime"
	"strings"

	"github.com/leezenn/slk/internal/auth"
	"github.com/spf13/cobra"
)

type authOptions struct {
	clear       bool
	interactive bool
}

func newAuthCommand(deps Dependencies, _ *rootOptions) *cobra.Command {
	options := &authOptions{}
	command := &cobra.Command{
		Use:   "auth [token]",
		Short: "Store or manage Slack API token",
		Long: `Store a Slack API token, show auth status, or clear stored credentials.

Requires a User OAuth Token (xoxp-). If the Slack app is already installed,
copy your token from OAuth & Permissions at https://api.slack.com/apps.
Authentication output is semantic text; the inherited --json flag has no effect.`,
		Example: `  slk auth xoxp-your-token-here    # Store token (non-interactive)
  slk auth                          # Show status or guided setup
  slk auth --interactive            # Reconnect interactively
  slk auth --clear                  # Remove stored token`,
		Args: argumentValidator(cobra.MaximumNArgs(1)),
	}
	command.Flags().BoolVar(&options.clear, "clear", false, "Remove stored token")
	command.Flags().BoolVar(&options.interactive, "interactive", false, "Prompt for a token even when one is already configured")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if options.interactive && (options.clear || len(args) > 0) {
			return conflictingOptions(cmd, "--interactive cannot be combined with --clear or a token argument")
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}
		store, err := deps.credentialStore()
		if err != nil {
			return err
		}
		if options.clear {
			if err := store.Clear(); err != nil {
				return credentialBackendError(err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Token removed from %s.\n", credStoreName())
			return nil
		}
		if options.interactive {
			return guidedSetup(cmd, deps, store, true)
		}
		if len(args) == 1 {
			return storeToken(cmd, deps, store, args[0])
		}

		result, err := store.Get()
		if err != nil {
			return guidedSetup(cmd, deps, store, false)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Status: configured")
		fmt.Fprintf(cmd.OutOrStdout(), "Source: %s\n", result.Source)
		fmt.Fprintf(cmd.OutOrStdout(), "Token:  %s\n", auth.MaskToken(result.Token))
		return nil
	}
	return command
}

func storeToken(cmd *cobra.Command, deps Dependencies, store auth.Store, raw string) error {
	token := strings.TrimSpace(raw)
	if token == "" {
		return invalidArgument(cmd, "empty token")
	}
	if !strings.HasPrefix(token, "xoxp-") {
		return invalidArgument(cmd, "expected a User OAuth Token (starts with xoxp-); bot tokens are not supported")
	}
	result, err := deps.validateToken(cmd.Context(), token, cmd.ErrOrStderr())
	if err != nil {
		return newCommandError(ErrorAuthFailed, "Slack rejected the credential.", "Run 'slk auth --interactive' to reconnect, then retry.")
	}
	if _, _, _, err := deps.bindIdentity(token, result); err != nil {
		return err
	}
	if err := store.Set(token); err != nil {
		return credentialBackendError(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Authenticated as @%s in %s.\n", result.User, result.Team)
	fmt.Fprintf(cmd.OutOrStdout(), "Token stored in %s.\n", credStoreName())
	for _, line := range authAccessSummary(result.Scopes) {
		fmt.Fprintln(cmd.OutOrStdout(), line)
	}
	return nil
}

func authAccessSummary(scopes []string) []string {
	if len(scopes) == 0 {
		return []string{"Access: Slack accepted the token but did not report enough information to verify feature permissions."}
	}

	granted := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		granted[scope] = true
	}

	requirements := []struct {
		feature string
		scopes  []string
	}{
		{feature: "public channels", scopes: []string{"channels:history", "channels:read"}},
		{feature: "private channels", scopes: []string{"groups:history", "groups:read"}},
		{feature: "direct messages", scopes: []string{"im:history", "im:read"}},
		{feature: "group messages", scopes: []string{"mpim:history", "mpim:read"}},
		{feature: "workspace discovery", scopes: []string{"search:read"}},
		{feature: "people and activity targeting", scopes: []string{"users:read"}},
		{feature: "file downloads", scopes: []string{"files:read"}},
	}

	var missingFeatures, missingScopes []string
	for _, requirement := range requirements {
		missing := false
		for _, scope := range requirement.scopes {
			if !granted[scope] {
				missing = true
				missingScopes = append(missingScopes, scope)
			}
		}
		if missing {
			missingFeatures = append(missingFeatures, requirement.feature)
		}
	}

	var summary []string
	if len(missingFeatures) == 0 {
		summary = append(summary, "Access: verified for all current slk read features.")
	} else {
		summary = append(summary,
			"Access is limited: "+humanList(missingFeatures)+" may not work.",
			"Missing Slack scopes: "+strings.Join(missingScopes, ", ")+".",
			"Update the Slack app permissions, reinstall it, then run 'slk auth --interactive'.",
		)
	}
	if granted["chat:write"] {
		summary = append(summary, "Message mutations: write, reply, edit, replace, and delete are available.")
	} else {
		summary = append(summary, "Message mutations: write, reply, edit, replace, and delete require chat:write.")
	}
	return summary
}

func humanList(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " and " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", and " + values[len(values)-1]
	}
}

func guidedSetup(cmd *cobra.Command, deps Dependencies, store auth.Store, reconnect bool) error {
	return guidedSetupWithReader(cmd, deps, store, reconnect, newCommandLineReader(cmd))
}

func guidedSetupWithReader(cmd *cobra.Command, deps Dependencies, store auth.Store, reconnect bool, reader *commandLineReader) error {
	out := cmd.OutOrStdout()
	if reconnect {
		fmt.Fprintln(out, "Let's reconnect Slack with a new token.")
	} else {
		fmt.Fprintln(out, "No token configured. Let's set one up.")
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "You need a Slack User OAuth Token (xoxp-...).")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "If your workspace already has a Slack app installed:")
	fmt.Fprintln(out, "  1. Go to https://api.slack.com/apps")
	fmt.Fprintln(out, "  2. Select your app")
	fmt.Fprintln(out, "  3. OAuth & Permissions -> User OAuth Token -> Copy")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "If not, create one:")
	fmt.Fprintln(out, "  1. https://api.slack.com/apps -> Create New App -> From scratch")
	fmt.Fprintln(out, "  2. OAuth & Permissions -> add these User Token Scopes:")
	fmt.Fprintln(out, "     channels:history, channels:read, groups:history, groups:read,")
	fmt.Fprintln(out, "     im:history, im:read, mpim:history, mpim:read,")
	fmt.Fprintln(out, "     search:read, users:read, files:read")
	fmt.Fprintln(out, "     Optional for write, reply, edit, replace, and delete: chat:write")
	fmt.Fprintln(out, "  3. Install to Workspace -> Copy User OAuth Token")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Token will be stored in %s.\n", credStoreName())
	fmt.Fprintln(out, "For non-interactive use: slk auth <token>")
	fmt.Fprintln(out, "Or set SLACK_TOKEN env var.")
	fmt.Fprintln(out)
	fmt.Fprint(out, "Paste your xoxp- token: ")

	token, err := reader.ReadLine()
	if err != nil {
		return err
	}
	return storeToken(cmd, deps, store, token)
}

type commandLineReader struct {
	cmd     *cobra.Command
	scanner *bufio.Scanner
}

func newCommandLineReader(cmd *cobra.Command) *commandLineReader {
	return &commandLineReader{cmd: cmd, scanner: bufio.NewScanner(cmd.InOrStdin())}
}

func (r *commandLineReader) ReadLine() (string, error) {
	type result struct {
		line string
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				completed <- result{err: newCommandError(
					ErrorInternal,
					"slk could not read from the terminal.",
					"Run the command and try again.",
				)}
				return
			}
			completed <- result{err: interruptedError()}
			return
		}
		completed <- result{line: r.scanner.Text()}
	}()

	select {
	case <-r.cmd.Context().Done():
		return "", interruptedError()
	case result := <-completed:
		if result.err != nil {
			return "", result.err
		}
		return result.line, nil
	}
}

func credStoreName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS Keychain"
	case "linux":
		return "Secret Service (GNOME Keyring)"
	case "windows":
		return "Windows Credential Manager"
	default:
		return "credential store"
	}
}
