package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

type writeOptions struct {
	text string
}

type writeClient interface {
	messagePostClient
	targetResolver
}

func newWriteCommand(deps Dependencies, rootOptions *rootOptions, prefix string) *cobra.Command {
	options := &writeOptions{}
	command := &cobra.Command{
		Use:   "write <channel-or-user>",
		Short: "Write a new top-level Slack message",
		Long: `Post one new top-level message to a Slack channel or existing DM.

The target may be a channel name or ID, an existing DM handle such as @alex,
a Slack user ID, or a DM channel ID. The command posts immediately. Confirm the
exact target and text before invoking it. Messages include the configured message
prefix as a small context line unless message_prefix is explicitly empty. Slack
must grant the current user token chat:write.`,
		Example: `  slk write general --text 'The deployment is complete.'
  slk write @alex --text 'Could you review the latest draft?'`,
		Args: argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().StringVar(&options.text, "text", "", "Exact message text")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return invalidArgument(cmd, "target must not be empty")
		}
		if strings.TrimSpace(options.text) == "" {
			return invalidArgument(cmd, "--text must contain the message")
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		return runWrite(cmd, rootOptions, client, args[0], options.text, prefix)
	}
	return command
}

func runWrite(cmd *cobra.Command, rootOptions *rootOptions, client writeClient, target, text, prefix string) error {
	channelID, _, err := resolveTarget(client, target)
	if err != nil {
		return slackAPIError(err)
	}
	return runMessagePost(cmd, rootOptions, client, messagePostTarget{
		channelID: channelID,
	}, text, prefix, postModeWrite)
}
