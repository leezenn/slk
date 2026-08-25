package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/leezenn/slk/internal/api"
	"github.com/spf13/cobra"
)

var version = "dev" // set by -ldflags at build time

type rootOptions struct {
	json    bool
	verbose bool
}

// NewRootCommand constructs a fresh command tree with invocation-local flags.
func NewRootCommand(deps Dependencies) *cobra.Command {
	options := &rootOptions{}
	root := &cobra.Command{
		Version:       version,
		Use:           "slk",
		Short:         "Explore Slack context and reply to message threads",
		SilenceErrors: true,
		SilenceUsage:  true,
		Long: `Explore Slack activity, channels, DMs, threads, and files, and reply to message threads.

Environment:
  SLACK_TOKEN  Fallback token if keychain is not configured`,
		Example: `  slk auth xoxp-your-token-here
  slk whoami
  slk activity
  slk activity @alex --since 8h
  slk recent --type dm
  slk channels --type dm
  slk read general --limit 50
  slk read @john --after 1d
  slk thread general 1705312325.000100
  slk search "deploy failed"
  slk download F0123456789
  slk reply '<slack-permalink>' --text 'We will ship the fix tomorrow.'

Tip: quoting short fragments from results helps users verify your interpretation.`,
	}
	root.PersistentFlags().BoolVar(&options.json, "json", false, "Output as JSON")
	root.PersistentFlags().BoolVarP(&options.verbose, "verbose", "v", false, "Show progress and detailed output")

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
		newReplyCommand(deps, options),
		newSearchCommand(deps, options),
		newThreadCommand(deps, options),
		newUsersCommand(deps, options),
		newWhoamiCommand(deps, options),
	)
	return root
}

// Execute runs one fresh command tree and returns its process exit code.
func Execute(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	return execute(DefaultDependencies(), ctx, args, in, out, errOut)
}

func execute(deps Dependencies, ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	root := NewRootCommand(deps)
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errOut)
	if err := root.ExecuteContext(ctx); err != nil {
		return renderError(err, root, args, errOut)
	}
	return 0
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
