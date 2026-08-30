package cmd

import (
	"errors"
	"fmt"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

type messagePostMode string

const (
	postModeReply messagePostMode = "reply"
	postModeWrite messagePostMode = "write"
)

type messagePostTarget struct {
	channelID      string
	threadTs       string
	replyBroadcast bool
}

type messagePostClient interface {
	PostMessage(request api.PostMessageRequest) (*api.PostMessageResult, error)
	GetPermalink(channelID, messageTs string) (string, error)
}

func runMessagePost(
	cmd *cobra.Command,
	rootOptions *rootOptions,
	client messagePostClient,
	target messagePostTarget,
	text string,
	prefix string,
	mode messagePostMode,
	formattingApplied []textformat.Module,
) error {
	posted, err := client.PostMessage(api.PostMessageRequest{
		ChannelID:      target.channelID,
		ThreadTs:       target.threadTs,
		Text:           text,
		Prefix:         prefix,
		ReplyBroadcast: target.replyBroadcast,
	})
	if err != nil {
		return messagePostError(err, mode)
	}

	permalink, permalinkErr := client.GetPermalink(posted.Channel, posted.Ts)
	if permalinkErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s posted, but Slack could not return its permalink: %s\n", postLabel(mode), safeDynamic(permalinkErr.Error(), 256))
	}

	if rootOptions.json {
		payload := map[string]interface{}{
			"ok":                 true,
			"posted":             true,
			"channel":            posted.Channel,
			"ts":                 posted.Ts,
			"permalink":          permalink,
			"open_command":       format.OpenCommand(permalink),
			"formatting_applied": formattingReceipt(formattingApplied),
		}
		if target.threadTs != "" {
			payload["thread_ts"] = target.threadTs
		}
		if mode == postModeReply {
			payload["reply_broadcast_requested"] = target.replyBroadcast
		}
		out, err := format.FormatJSON(payload)
		if err != nil {
			return internalError()
		}
		fmt.Fprintln(cmd.OutOrStdout(), out)
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s posted.\n", postReceiptLabel(mode))
	if mode == postModeReply && target.replyBroadcast {
		fmt.Fprintln(cmd.OutOrStdout(), "Conversation broadcast requested.")
	}
	writeFormattingNotice(cmd, formattingApplied)
	if permalink != "" {
		fmt.Fprintln(cmd.OutOrStdout(), permalink)
		fmt.Fprintf(cmd.OutOrStdout(), "Open: %s\n", format.OpenCommand(permalink))
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Channel: %s\nTimestamp: %s\n", posted.Channel, posted.Ts)
	if target.threadTs != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Thread: slk thread %s %s\n", posted.Channel, target.threadTs)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Context: slk read %s --around %s\n", posted.Channel, posted.Ts)
	}
	return nil
}

func replyPostError(err error) error {
	return messagePostError(err, postModeReply)
}

func writePostError(err error) error {
	return messagePostError(err, postModeWrite)
}

func messagePostError(err error, mode messagePostMode) error {
	var methodErr *api.MethodError
	if !errors.As(err, &methodErr) || methodErr.Code == "internal_error" || methodErr.Code == "fatal_error" {
		return newCommandError(
			ErrorSlackAPI,
			fmt.Sprintf("Slack did not confirm whether the %s was posted.", postLabel(mode)),
			fmt.Sprintf("Inspect the %s before retrying.", inspectionTarget(mode)),
		)
	}

	switch methodErr.Code {
	case "missing_scope":
		return newCommandError(
			ErrorAuthFailed,
			fmt.Sprintf("Slack cannot post %s with the current credential.", postPlural(mode)),
			"Add chat:write to the Slack app, reinstall it, then run 'slk auth --interactive'.",
		)
	case "invalid_auth", "not_authed", "token_expired", "token_revoked", "account_inactive":
		return newCommandError(
			ErrorAuthFailed,
			"Slack rejected the credential.",
			"Run 'slk auth --interactive' to reconnect, then retry.",
		)
	case "not_in_channel":
		message := "Slack cannot post the message because the authenticated user is not in that conversation."
		if mode == postModeReply {
			message = "Slack cannot reply because the authenticated user is not in that conversation."
		}
		return newCommandError(
			ErrorSlackAPI,
			message,
			"Open or join the conversation, then retry.",
		)
	case "channel_not_found":
		if mode == postModeReply {
			return newCommandError(
				ErrorSlackAPI,
				"Slack could not find the message thread.",
				"Open the permalink in Slack and verify the message still exists.",
			)
		}
		return newCommandError(
			ErrorSlackAPI,
			"Slack could not find the target conversation.",
			"Verify the channel or DM target before retrying.",
		)
	case "message_not_found":
		if mode == postModeReply {
			return newCommandError(
				ErrorSlackAPI,
				"Slack could not find the message thread.",
				"Open the permalink in Slack and verify the message still exists.",
			)
		}
	case "cannot_reply_to_message", "restricted_action_non_threadable_channel", "restricted_action_thread_locked":
		if mode == postModeReply {
			return newCommandError(
				ErrorSlackAPI,
				"Slack does not allow replies in this thread.",
				"Inspect the conversation restrictions before choosing another action.",
			)
		}
	case "restricted_action", "restricted_action_read_only_channel":
		message := "Slack does not allow this message in the target conversation."
		if mode == postModeReply {
			message = "Slack does not allow replies in this thread."
		}
		return newCommandError(
			ErrorSlackAPI,
			message,
			"Inspect the conversation restrictions before choosing another action.",
		)
	}

	return newCommandError(
		ErrorSlackAPI,
		fmt.Sprintf("Slack rejected the %s: %s.", postLabel(mode), safeDynamic(methodErr.Code, 256)),
		fmt.Sprintf("Inspect the %s and Slack permissions before retrying.", inspectionTarget(mode)),
	)
}

func postLabel(mode messagePostMode) string {
	if mode == postModeReply {
		return "reply"
	}
	return "message"
}

func postPlural(mode messagePostMode) string {
	if mode == postModeReply {
		return "replies"
	}
	return "messages"
}

func postReceiptLabel(mode messagePostMode) string {
	if mode == postModeReply {
		return "Reply"
	}
	return "Message"
}

func inspectionTarget(mode messagePostMode) string {
	if mode == postModeReply {
		return "thread"
	}
	return "conversation"
}
