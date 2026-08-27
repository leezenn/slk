package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/config"
	"github.com/spf13/cobra"
)

var version = "dev" // set by -ldflags at build time

type rootOptions struct {
	json    bool
	verbose bool
}

// NewRootCommand constructs a fresh unrestricted command tree for embedding and tests.
func NewRootCommand(deps Dependencies) *cobra.Command {
	return newRootCommand(deps, config.Defaults())
}

func newRootCommand(deps Dependencies, settings config.Settings) *cobra.Command {
	options := &rootOptions{}
	root := &cobra.Command{
		Version:       version,
		Use:           "slk",
		Short:         rootShort(settings),
		SilenceErrors: true,
		SilenceUsage:  true,
		Long:          rootLong(settings),
		Example:       rootExamples(settings),
	}
	root.PersistentFlags().BoolVar(&options.json, "json", false, "Output as JSON")
	root.PersistentFlags().BoolVarP(&options.verbose, "verbose", "v", false, "Show progress and detailed output")
	root.AddCommand(newConfigCommand(deps, options))
	if settings.Disabled {
		return root
	}

	root.AddCommand(
		newActivityCommand(deps, options),
		newAuthCommand(deps, options),
		newChannelsCommand(deps, options),
		newDownloadCommand(deps, options),
		newMembersCommand(deps, options),
		newNotesCommand(deps, options),
		newOpenCommand(deps, options),
		newReadCommand(deps, options),
		newRecentCommand(deps, options),
		newSearchCommand(deps, options),
		newThreadCommand(deps, options),
		newUsersCommand(deps, options),
		newWhoamiCommand(deps, options),
	)
	if !settings.MutationDenied(config.MutationReply) {
		root.AddCommand(newReplyCommand(deps, options, settings.MessagePrefix))
	}
	if !settings.MutationDenied(config.MutationWrite) {
		root.AddCommand(newWriteCommand(deps, options, settings.MessagePrefix))
	}
	return root
}

func rootShort(settings config.Settings) string {
	if settings.Disabled {
		return "slk is disabled by local configuration"
	}
	hasReply := !settings.MutationDenied(config.MutationReply)
	hasWrite := !settings.MutationDenied(config.MutationWrite)
	switch {
	case hasReply && hasWrite:
		return "Explore Slack context and post messages"
	case hasReply:
		return "Explore Slack context and reply to message threads"
	case hasWrite:
		return "Explore Slack context and write messages"
	default:
		return "Explore Slack context"
	}
}

func rootLong(settings config.Settings) string {
	if settings.Disabled {
		return `slk is disabled by local configuration.

Slack operational commands are hidden and blocked. Stored and environment
credentials are ignored while disabled.

Agent guidance:
  Do not enable slk autonomously. Ask the user for permission before running:

    slk config enable

Configuration:
  $XDG_CONFIG_HOME/slk/config.json (defaults to ~/.config/slk/config.json)`
	}
	description := "Explore Slack activity, channels, DMs, threads, and files"
	hasReply := !settings.MutationDenied(config.MutationReply)
	hasWrite := !settings.MutationDenied(config.MutationWrite)
	switch {
	case hasReply && hasWrite:
		description += ", write top-level messages, and reply to message threads"
	case hasReply:
		description += ", and reply to message threads"
	case hasWrite:
		description += ", and write top-level messages"
	default:
		description += "."
	}
	if hasReply || hasWrite {
		description += "."
	}
	return description + `

Environment:
  SLACK_TOKEN       Fallback token if keychain is not configured
  XDG_CONFIG_HOME   Config root; defaults to ~/.config

Configuration:
  $XDG_CONFIG_HOME/slk/config.json`
}

func rootExamples(settings config.Settings) string {
	if settings.Disabled {
		return `  slk config
  slk config enable   # Only after explicit user permission`
	}
	examples := []string{
		"  slk auth xoxp-your-token-here",
		"  slk whoami",
		"  slk activity",
		"  slk activity @alex --since 8h",
		"  slk recent --type dm",
		"  slk channels --type dm",
		"  slk read general --limit 50",
		"  slk read @john --after 1d",
		"  slk thread general 1705312325.000100",
		`  slk search "deploy failed"`,
		"  slk download F0123456789",
	}
	if !settings.MutationDenied(config.MutationWrite) {
		examples = append(examples, "  slk write general --text 'The deployment is complete.'")
	}
	if !settings.MutationDenied(config.MutationReply) {
		examples = append(examples, "  slk reply '<slack-permalink>' --text 'We will ship the fix tomorrow.'")
	}
	return strings.Join(examples, "\n") + "\n\nTip: quoting short fragments from results helps users verify your interpretation."
}

// Execute runs one fresh command tree and returns its process exit code.
func Execute(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	return execute(DefaultDependencies(), ctx, args, in, out, errOut)
}

func execute(deps Dependencies, ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	if err := checkContext(ctx); err != nil {
		root := configuredRoot(NewRootCommand(deps), args, in, out, errOut)
		return renderError(err, root, args, errOut)
	}
	settings, err := deps.config()
	if err != nil {
		root := configuredRoot(NewRootCommand(deps), args, in, out, errOut)
		return renderError(err, root, args, errOut)
	}
	root := configuredRoot(newRootCommand(deps, settings), args, in, out, errOut)
	if command, blocked := disabledCommandFromArgs(args, settings); blocked {
		return renderError(toolDisabledError(command), root, args, errOut)
	}
	if mutation, denied := deniedMutationFromArgs(args, settings); denied {
		return renderError(mutationDeniedError(mutation), root, args, errOut)
	}
	if err := root.ExecuteContext(ctx); err != nil {
		return renderError(err, root, args, errOut)
	}
	return 0
}

func configuredRoot(root *cobra.Command, args []string, in io.Reader, out, errOut io.Writer) *cobra.Command {
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errOut)
	return root
}

func disabledCommandFromArgs(args []string, settings config.Settings) (string, bool) {
	if !settings.Disabled {
		return "", false
	}
	command := commandNameFromArgs(args)
	switch command {
	case "", "config", "help", "completion":
		return "", false
	default:
		return command, true
	}
}

func deniedMutationFromArgs(args []string, settings config.Settings) (config.Mutation, bool) {
	mutation, known := config.ParseMutation(commandNameFromArgs(args))
	return mutation, known && settings.MutationDenied(mutation)
}

func commandNameFromArgs(args []string) string {
	first := ""
	for _, arg := range args {
		switch {
		case arg == "--":
			continue
		case arg == "--json" || arg == "--verbose" || arg == "-v":
			continue
		case strings.HasPrefix(arg, "--json=") || strings.HasPrefix(arg, "--verbose=") || strings.HasPrefix(arg, "-v="):
			continue
		case strings.HasPrefix(arg, "-"):
			if first == "" {
				return ""
			}
			continue
		case first == "help":
			return arg
		case first == "":
			first = arg
			if first != "help" {
				return first
			}
		}
	}
	return first
}

type selfIdentifier interface {
	Identify() error
	SelfID() string
}

func identifySelf(client selfIdentifier) (string, error) {
	if err := client.Identify(); err != nil {
		return "", fmt.Errorf("identifying authenticated Slack user: %w", err)
	}
	if client.SelfID() == "" {
		return "", fmt.Errorf("identifying authenticated Slack user: Slack returned an empty user ID")
	}
	return client.SelfID(), nil
}

func getClient(cmd *cobra.Command, deps Dependencies) (*api.Client, error) {
	if err := checkContext(cmd.Context()); err != nil {
		return nil, err
	}
	store, err := deps.credentialStore()
	if err != nil {
		return nil, err
	}
	result, err := store.Get()
	if err != nil {
		return nil, authRequiredError()
	}
	client, err := deps.client(result.Token)
	if err != nil {
		return nil, err
	}
	client.SetContext(cmd.Context())
	client.SetErrorWriter(cmd.ErrOrStderr())
	return client, nil
}
