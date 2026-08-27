package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/leezenn/slk/internal/api"
)

type messageMutationKind string

const (
	mutationKindDelete  messageMutationKind = "delete"
	mutationKindEdit    messageMutationKind = "edit"
	mutationKindReplace messageMutationKind = "replace"
)

type messageMutationTarget struct {
	channelID string
	messageTs string
	threadTs  string
	permalink string
}

type messageOwnershipClient interface {
	selfIdentifier
	GetMessage(channelID, messageTs string) (*api.Message, error)
	GetReply(channelID, threadTs, messageTs string) (*api.Message, error)
}

func parseMessageMutationTarget(rawPermalink string) (messageMutationTarget, error) {
	parsed, err := parsePermalink(rawPermalink)
	if err != nil {
		return messageMutationTarget{}, err
	}
	if !slackTimestampRe.MatchString(parsed.messageTs) {
		return messageMutationTarget{}, fmt.Errorf("permalink has an invalid message timestamp")
	}
	if parsed.threadTs != "" && !slackTimestampRe.MatchString(parsed.threadTs) {
		return messageMutationTarget{}, fmt.Errorf("permalink has an invalid thread timestamp")
	}
	return messageMutationTarget{
		channelID: parsed.channelID,
		messageTs: parsed.messageTs,
		threadTs:  parsed.threadTs,
		permalink: strings.Trim(strings.TrimSpace(rawPermalink), "<>"),
	}, nil
}

func ownedMessageForMutation(client messageOwnershipClient, target messageMutationTarget, kind messageMutationKind) (*api.Message, error) {
	selfID, err := identifySelf(client)
	if err != nil {
		return nil, slackAPIError(err)
	}
	message, err := messageForMutation(client, target, kind)
	if err != nil {
		return nil, err
	}
	if message.User == "" || message.User != selfID {
		return nil, refusedError(
			"slk refuses to "+string(kind)+" a message not authored by the authenticated user.",
			"Target a message authored by the current Slack user.",
		)
	}
	return message, nil
}

func messageForMutation(client messageOwnershipClient, target messageMutationTarget, kind messageMutationKind) (*api.Message, error) {
	var message *api.Message
	var err error
	if target.threadTs != "" && target.messageTs != target.threadTs {
		message, err = client.GetReply(target.channelID, target.threadTs, target.messageTs)
	} else {
		message, err = client.GetMessage(target.channelID, target.messageTs)
	}
	if err != nil {
		return nil, newCommandError(
			ErrorSlackAPI,
			"slk could not verify the target message before attempting to "+string(kind)+" it.",
			"Open the permalink and verify the message still exists and is visible, then retry.",
		)
	}
	return message, nil
}

func messageMutationError(err error, kind messageMutationKind) error {
	var methodErr *api.MethodError
	if !errors.As(err, &methodErr) || methodErr.Code == "internal_error" || methodErr.Code == "fatal_error" {
		return newCommandError(
			ErrorSlackAPI,
			fmt.Sprintf("Slack did not confirm whether the message was %s.", mutationPastTense(kind)),
			"Open the permalink and inspect the exact message before retrying.",
		)
	}

	switch methodErr.Code {
	case "missing_scope":
		return newCommandError(
			ErrorAuthFailed,
			fmt.Sprintf("Slack cannot %s messages with the current credential.", kind),
			"Add chat:write to the Slack app, reinstall it, then run 'slk auth --interactive'.",
		)
	case "invalid_auth", "not_authed", "token_expired", "token_revoked", "account_inactive":
		return newCommandError(
			ErrorAuthFailed,
			"Slack rejected the credential.",
			"Run 'slk auth --interactive' to reconnect, then retry.",
		)
	case "cant_update_message":
		if kind == mutationKindReplace || kind == mutationKindEdit {
			return refusedError(
				"Slack does not allow the authenticated user to modify this message.",
				"Verify the message was authored by the current user and remains editable.",
			)
		}
	case "cant_delete_message":
		if kind == mutationKindDelete {
			return refusedError(
				"Slack does not allow the authenticated user to delete this message.",
				"Verify the message was authored by the current user and can be deleted in Slack.",
			)
		}
	case "edit_window_closed":
		if kind == mutationKindReplace || kind == mutationKindEdit {
			return refusedError(
				"The workspace message-edit window has closed for this message.",
				"Keep the existing message or post a new correction.",
			)
		}
	case "message_not_found", "channel_not_found":
		return newCommandError(
			ErrorSlackAPI,
			"Slack could not find the target message.",
			"Open the permalink and verify the message still exists before retrying.",
		)
	}

	return newCommandError(
		ErrorSlackAPI,
		fmt.Sprintf("Slack rejected the message %s: %s.", kind, safeDynamic(methodErr.Code, 256)),
		"Inspect the target message and Slack permissions before retrying.",
	)
}

func mutationPastTense(kind messageMutationKind) string {
	switch kind {
	case mutationKindEdit:
		return "edited"
	case mutationKindReplace:
		return "replaced"
	default:
		return "deleted"
	}
}
