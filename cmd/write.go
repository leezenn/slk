package cmd

import (
	"strings"

	"github.com/leezenn/slk/internal/presentation"
	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

type writeOptions struct {
	text         string
	presentation string
}

type writeClient interface {
	messagePostClient
	targetResolver
}

func newWriteCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &writeOptions{}
	command := &cobra.Command{
		Use:   "write <channel-or-user>",
		Short: "Write a new top-level Slack message",
		Long: `Post one new top-level message to a Slack channel or existing DM.

The target may be a channel name or ID, an existing DM handle such as @alex,
a Slack user ID, or a DM channel ID. The command posts immediately. Confirm the
exact target and text before invoking it. Messages include the configured message
prefix as a small context line unless message_prefix is explicitly empty. Slack
must grant the current user token chat:write.` + presentationHelp() + formattingHelp("The --text value"),
		Example: `  slk write general --text 'The deployment is complete.'
  slk write @alex --text 'Could you review the latest draft?'`,
		Args: argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().StringVar(&options.text, "text", "", "Exact message text")
	addPresentationFlag(command, &options.presentation)
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return invalidArgument(cmd, "target must not be empty")
		}
		if strings.TrimSpace(options.text) == "" {
			return invalidArgument(cmd, "--text must contain the message")
		}
		if _, err := resolvePresentation(cmd, options.presentation, presentation.Default()); err != nil {
			return err
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}
		bound, settings, err := bindCommandIdentity(cmd, deps)
		if err != nil {
			return err
		}
		mode, _ := resolvePresentation(cmd, options.presentation, settings.MessagePresentation)
		client, err := getClient(cmd, bound)
		if err != nil {
			return err
		}
		return runWrite(cmd, rootOptions, client, args[0], options.text, settings.MessagePrefix, mode, settings.Formatting...)
	}
	return command
}

func runWrite(cmd *cobra.Command, rootOptions *rootOptions, client writeClient, target, text, prefix string, mode presentation.Mode, modules ...textformat.Module) error {
	channelID, _, err := resolveTarget(client, target)
	if err != nil {
		return slackAPIError(err)
	}
	formatted := textformat.Apply(text, modules)
	return runMessagePost(cmd, rootOptions, client, messagePostTarget{
		channelID: channelID,
	}, formatted.Text, prefix, mode, postModeWrite, formatted.Applied)
}
