package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/leezenn/slk/internal/presentation"
	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

var slackTimestampRe = regexp.MustCompile(`^[0-9]+\.[0-9]{6}$`)

type replyOptions struct {
	text                   string
	presentation           string
	alsoSendToConversation bool
}

type replyTarget struct {
	channelID string
	threadTs  string
}

func newReplyCommand(deps Dependencies, rootOptions *rootOptions, prefix string, configuredPresentation presentation.Mode, formatting ...textformat.Module) *cobra.Command {
	options := &replyOptions{}
	command := &cobra.Command{
		Use:   "reply <slack-permalink>",
		Short: "Reply to a Slack message thread",
		Long: `Post one reply to the thread identified by a Slack message permalink.

The command posts immediately. Read the conversation first and provide the exact
reply text with --text. Use --also-send-to-conversation only when the reply is
important enough to surface in the main channel or DM timeline; the message
remains in its thread. Replies include the configured message prefix as a small
context line unless message_prefix is explicitly empty. Slack must grant the
current user token chat:write.` + presentationHelp(configuredPresentation) + formattingHelp(formatting, "The --text value"),
		Example: `  slk reply 'https://workspace.slack.com/archives/C12345/p1705312325000100' --text 'We found the issue and will ship the fix tomorrow.'
  slk reply '<slack-permalink>' --text 'Important update.' --also-send-to-conversation`,
		Args: argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().StringVar(&options.text, "text", "", "Exact reply text")
	addPresentationFlag(command, &options.presentation)
	command.Flags().BoolVar(&options.alsoSendToConversation, "also-send-to-conversation", false, "Also surface the reply in the main conversation timeline")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(options.text) == "" {
			return invalidArgument(cmd, "--text must contain the reply")
		}
		target, err := parseReplyTarget(args[0])
		if err != nil {
			return invalidArgument(cmd, err.Error())
		}
		mode, err := resolvePresentation(cmd, options.presentation, configuredPresentation)
		if err != nil {
			return err
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		return runReply(cmd, rootOptions, client, target, options.text, prefix, mode, options.alsoSendToConversation, formatting...)
	}
	return command
}

func runReply(cmd *cobra.Command, rootOptions *rootOptions, client messagePostClient, target replyTarget, text, prefix string, mode presentation.Mode, replyBroadcast bool, modules ...textformat.Module) error {
	formatted := textformat.Apply(text, modules)
	return runMessagePost(cmd, rootOptions, client, messagePostTarget{
		channelID:      target.channelID,
		threadTs:       target.threadTs,
		replyBroadcast: replyBroadcast,
	}, formatted.Text, prefix, mode, postModeReply, formatted.Applied)
}

func parseReplyTarget(permalink string) (replyTarget, error) {
	parsed, err := parsePermalink(permalink)
	if err != nil {
		return replyTarget{}, err
	}
	threadTs := parsed.messageTs
	if parsed.threadTs != "" {
		if !slackTimestampRe.MatchString(parsed.threadTs) {
			return replyTarget{}, fmt.Errorf("permalink has an invalid thread timestamp")
		}
		threadTs = parsed.threadTs
	}
	return replyTarget{channelID: parsed.channelID, threadTs: threadTs}, nil
}
