package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var slackTimestampRe = regexp.MustCompile(`^[0-9]+\.[0-9]{6}$`)

type replyOptions struct {
	text string
}

type replyTarget struct {
	channelID string
	threadTs  string
}

type replyClient interface {
	PostReply(channelID, threadTs, text string) (*api.PostMessageResult, error)
	GetPermalink(channelID, messageTs string) (string, error)
}

func newReplyCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &replyOptions{}
	command := &cobra.Command{
		Use:   "reply <slack-permalink>",
		Short: "Reply to a Slack message thread",
		Long: `Post one reply to the thread identified by a Slack message permalink.

The command posts immediately. Read the conversation first and provide the exact
reply text with --text. Slack must grant the current user token chat:write.`,
		Example: `  slk reply 'https://workspace.slack.com/archives/C12345/p1705312325000100' --text 'We found the issue and will ship the fix tomorrow.'`,
		Args:    argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().StringVar(&options.text, "text", "", "Exact reply text")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(options.text) == "" {
			return invalidArgument(cmd, "--text must contain the reply")
		}
		target, err := parseReplyTarget(args[0])
		if err != nil {
			return invalidArgument(cmd, err.Error())
		}
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		return runReply(cmd, rootOptions, client, target, options.text)
	}
	return command
}

func runReply(cmd *cobra.Command, rootOptions *rootOptions, client replyClient, target replyTarget, text string) error {
	posted, err := client.PostReply(target.channelID, target.threadTs, text)
	if err != nil {
		return replyPostError(err)
	}

	permalink, permalinkErr := client.GetPermalink(posted.Channel, posted.Ts)
	if permalinkErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: reply posted, but Slack could not return its permalink: %s\n", safeDynamic(permalinkErr.Error(), 256))
	}

	if rootOptions.json {
		out, err := format.FormatJSON(map[string]interface{}{
			"ok":           true,
			"posted":       true,
			"channel":      posted.Channel,
			"thread_ts":    target.threadTs,
			"ts":           posted.Ts,
			"permalink":    permalink,
			"open_command": format.OpenCommand(permalink),
		})
		if err != nil {
			return internalError()
		}
		fmt.Fprintln(cmd.OutOrStdout(), out)
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Reply posted.")
	if permalink != "" {
		fmt.Fprintln(cmd.OutOrStdout(), permalink)
		fmt.Fprintf(cmd.OutOrStdout(), "Open: %s\n", format.OpenCommand(permalink))
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Channel: %s\nTimestamp: %s\n", posted.Channel, posted.Ts)
		fmt.Fprintf(cmd.OutOrStdout(), "Thread: slk thread %s %s\n", posted.Channel, target.threadTs)
	}
	return nil
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

func replyPostError(err error) error {
	var methodErr *api.MethodError
	if !errors.As(err, &methodErr) {
		return newCommandError(
			ErrorSlackAPI,
			"Slack did not confirm whether the reply was posted.",
			"Inspect the thread before retrying.",
		)
	}

	switch methodErr.Code {
	case "internal_error", "fatal_error":
		return newCommandError(
			ErrorSlackAPI,
			"Slack did not confirm whether the reply was posted.",
			"Inspect the thread before retrying.",
		)
	case "missing_scope":
		return newCommandError(
			ErrorAuthFailed,
			"Slack cannot post replies with the current credential.",
			"Add chat:write to the Slack app, reinstall it, then run 'slk auth --interactive'.",
		)
	case "invalid_auth", "not_authed", "token_expired", "token_revoked", "account_inactive":
		return newCommandError(
			ErrorAuthFailed,
			"Slack rejected the credential.",
			"Run 'slk auth --interactive' to reconnect, then retry.",
		)
	case "not_in_channel":
		return newCommandError(
			ErrorSlackAPI,
			"Slack cannot reply because the authenticated user is not in that conversation.",
			"Open or join the conversation, then retry.",
		)
	case "channel_not_found", "message_not_found":
		return newCommandError(
			ErrorSlackAPI,
			"Slack could not find the message thread.",
			"Open the permalink in Slack and verify the message still exists.",
		)
	case "cannot_reply_to_message", "restricted_action", "restricted_action_non_threadable_channel", "restricted_action_read_only_channel", "restricted_action_thread_locked":
		return newCommandError(
			ErrorSlackAPI,
			"Slack does not allow replies in this thread.",
			"Inspect the conversation restrictions before choosing another action.",
		)
	default:
		return newCommandError(
			ErrorSlackAPI,
			"Slack rejected the reply: "+safeDynamic(methodErr.Code, 256)+".",
			"Inspect the thread and Slack permissions before retrying.",
		)
	}
}
