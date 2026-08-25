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
	clear bool
}

func newAuthCommand(deps Dependencies, _ *rootOptions) *cobra.Command {
	options := &authOptions{}
	command := &cobra.Command{
		Use:   "auth [token]",
		Short: "Store or manage Slack API token",
		Long: `Store a Slack API token, show auth status, or clear stored credentials.

Requires a User OAuth Token (xoxp-). If the Slack app is already installed,
copy your token from OAuth & Permissions at https://api.slack.com/apps.`,
		Example: `  slk auth xoxp-your-token-here    # Store token (non-interactive)
  slk auth                          # Show status or guided setup
  slk auth --clear                  # Remove stored token`,
		Args: argumentValidator(cobra.MaximumNArgs(1)),
	}
	command.Flags().BoolVar(&options.clear, "clear", false, "Remove stored token")
	command.RunE = func(cmd *cobra.Command, args []string) error {
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
		if len(args) == 1 {
			return storeToken(cmd, deps, store, args[0])
		}

		result, err := store.Get()
		if err != nil {
			return guidedSetup(cmd, deps, store)
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
	client, err := deps.client(token)
	if err != nil {
		return err
	}
	client.SetContext(cmd.Context())
	client.SetErrorWriter(cmd.ErrOrStderr())
	result, err := client.AuthTest()
	if err != nil {
		return newCommandError(ErrorAuthFailed, "Slack rejected the credential.", "Run 'slk auth' to reconnect, then retry.")
	}
	if err := store.Set(token); err != nil {
		return credentialBackendError(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Authenticated as @%s in %s.\n", result.User, result.Team)
	fmt.Fprintf(cmd.OutOrStdout(), "Token stored in %s.\n", credStoreName())
	return nil
}

func guidedSetup(cmd *cobra.Command, deps Dependencies, store auth.Store) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "No token configured. Let's set one up.")
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
	fmt.Fprintln(out, "  3. Install to Workspace -> Copy User OAuth Token")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Token will be stored in %s.\n", credStoreName())
	fmt.Fprintln(out, "For non-interactive use: slk auth <token>")
	fmt.Fprintln(out, "Or set SLACK_TOKEN env var.")
	fmt.Fprintln(out)
	fmt.Fprint(out, "Paste your xoxp- token: ")

	token, err := readLine(cmd)
	if err != nil {
		return err
	}
	return storeToken(cmd, deps, store, token)
}

func readLine(cmd *cobra.Command) (string, error) {
	type result struct {
		line string
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(cmd.InOrStdin())
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				completed <- result{err: newCommandError(
					ErrorInternal,
					"slk could not read the token from the terminal.",
					"Run 'slk auth' and try again.",
				)}
				return
			}
			completed <- result{err: interruptedError()}
			return
		}
		completed <- result{line: scanner.Text()}
	}()

	select {
	case <-cmd.Context().Done():
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
