package cmd

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

type openOptions struct {
	contextBefore int
}

var permalinkRe = regexp.MustCompile(`^https://[^/]+\.slack\.com/archives/([A-Z0-9]+)/p(\d{10})(\d{6})`)

type parsedPermalink struct {
	channelID string
	messageTs string
	threadTs  string
}

func parsePermalink(rawURL string) (*parsedPermalink, error) {
	rawURL = strings.TrimPrefix(rawURL, "<")
	rawURL = strings.TrimSuffix(rawURL, ">")
	match := permalinkRe.FindStringSubmatch(rawURL)
	if match == nil {
		return nil, fmt.Errorf("not a valid Slack permalink")
	}
	parsed := &parsedPermalink{channelID: match[1], messageTs: match[2] + "." + match[3]}
	urlValue, err := url.Parse(rawURL)
	if err == nil && urlValue.Query().Get("thread_ts") != "" {
		parsed.threadTs = urlValue.Query().Get("thread_ts")
	}
	return parsed, nil
}

func newOpenCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &openOptions{contextBefore: 2}
	command := &cobra.Command{
		Use:   "open <slack-url>",
		Short: "Open a Slack message permalink and display it with context",
		Long: `Open a Slack message permalink and display the message with surrounding context.

Parses standard Slack permalinks and fetches the referenced message along with
preceding messages for context. Supports both channel messages and thread replies.`,
		Example: `  slk open https://workspace.slack.com/archives/C12345/p1705312325000100
  slk open "https://workspace.slack.com/archives/C12345/p1705312325000100?thread_ts=1705312300.000050&cid=C12345"
  slk open https://workspace.slack.com/archives/C12345/p1705312325000100 --context 5`,
		Args: argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().IntVar(&options.contextBefore, "context", 2, "Number of messages before the target for context")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		rawURL := args[0]
		permalink, err := parsePermalink(rawURL)
		if err != nil {
			return invalidArgument(cmd, err.Error())
		}
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		selfID, err := identifySelf(client)
		if err != nil {
			return slackAPIError(err)
		}
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: user cache unavailable: %v\n", err)
		}

		var messages []api.Message
		channelName := permalink.channelID
		if permalink.threadTs != "" {
			threadMessages, err := client.GetReplies(permalink.channelID, permalink.threadTs, 0)
			if err != nil {
				return slackAPIError(err)
			}
			targetIndex := -1
			for i, message := range threadMessages {
				if message.Ts == permalink.messageTs {
					targetIndex = i
					break
				}
			}
			if targetIndex == -1 {
				messages = threadMessages
			} else {
				start := targetIndex - options.contextBefore
				if start < 0 {
					start = 0
				}
				messages = threadMessages[start : targetIndex+1]
			}
		} else {
			if options.contextBefore > 0 {
				contextMessages, err := client.GetContext(permalink.channelID, permalink.messageTs, options.contextBefore)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not fetch context: %v\n", err)
				} else {
					reverseMessages(contextMessages)
					messages = append(messages, contextMessages...)
				}
			}
			targetMessage, err := client.GetMessage(permalink.channelID, permalink.messageTs)
			if err != nil {
				return slackAPIError(err)
			}
			messages = append(messages, *targetMessage)
		}

		if rootOptions.json {
			payload := map[string]interface{}{
				"ok": true, "channel": channelName,
				"messages": format.MessagesToJSON(messages, client.ResolveUser, selfID),
				"url":      rawURL,
			}
			if permalink.threadTs != "" {
				payload["thread_ts"] = permalink.threadTs
			}
			out, err := format.FormatJSON(payload)
			if err != nil {
				return internalError()
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), format.FormatMessages(messages, channelName, client.ResolveUser, selfID))
		return nil
	}
	return command
}
