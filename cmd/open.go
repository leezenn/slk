package cmd

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var contextBefore int

// permalinkRe matches Slack message permalinks:
// https://<workspace>.slack.com/archives/<channel_id>/p<timestamp_without_dot>
var permalinkRe = regexp.MustCompile(`^https://[^/]+\.slack\.com/archives/([A-Z0-9]+)/p(\d{10})(\d{6})`)

type parsedPermalink struct {
	channelID string
	messageTs string
	threadTs  string // non-empty if this is a thread reply link
}

// parsePermalink extracts channel ID, message ts, and optional thread_ts from a Slack permalink.
func parsePermalink(rawURL string) (*parsedPermalink, error) {
	// Strip angle brackets (users may paste <https://...>)
	rawURL = strings.TrimPrefix(rawURL, "<")
	rawURL = strings.TrimSuffix(rawURL, ">")

	m := permalinkRe.FindStringSubmatch(rawURL)
	if m == nil {
		return nil, fmt.Errorf("not a valid Slack permalink: %s", rawURL)
	}

	p := &parsedPermalink{
		channelID: m[1],
		messageTs: m[2] + "." + m[3],
	}

	// Check for thread_ts in query params
	parsed, err := url.Parse(rawURL)
	if err == nil && parsed.Query().Get("thread_ts") != "" {
		p.threadTs = parsed.Query().Get("thread_ts")
	}

	return p, nil
}

var openCmd = &cobra.Command{
	Use:   "open <slack-url>",
	Short: "Open a Slack message permalink and display it with context",
	Long: `Open a Slack message permalink and display the message with surrounding context.

Parses standard Slack permalinks and fetches the referenced message along with
preceding messages for context. Supports both channel messages and thread replies.`,
	Example: `  slk open https://workspace.slack.com/archives/C12345/p1705312325000100
  slk open "https://workspace.slack.com/archives/C12345/p1705312325000100?thread_ts=1705312300.000050&cid=C12345"
  slk open https://workspace.slack.com/archives/C12345/p1705312325000100 --context 5`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rawURL := args[0]

		p, err := parsePermalink(rawURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		result, err := auth.GetToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		client := api.NewClient(result.Token)
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: user cache unavailable: %v\n", err)
		}

		var msgs []api.Message
		var channelName string

		// Use channel ID as display name (resolveChannel would need an API call)
		channelName = p.channelID

		if p.threadTs != "" {
			// Thread reply: fetch the thread around this message
			threadMsgs, err := client.GetReplies(p.channelID, p.threadTs, 0)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching thread: %v\n", err)
				os.Exit(1)
			}

			// Find the target message in the thread and include context
			targetIdx := -1
			for i, m := range threadMsgs {
				if m.Ts == p.messageTs {
					targetIdx = i
					break
				}
			}

			if targetIdx == -1 {
				// Target not found in thread; show what we have
				msgs = threadMsgs
			} else {
				// Show contextBefore messages before target, plus target
				start := targetIdx - contextBefore
				if start < 0 {
					start = 0
				}
				msgs = threadMsgs[start : targetIdx+1]
			}
		} else {
			// Channel message: fetch context before, then the target message
			if contextBefore > 0 {
				ctxMsgs, err := client.GetContext(p.channelID, p.messageTs, contextBefore)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not fetch context: %v\n", err)
				} else {
					// GetContext returns newest-first, reverse for chronological
					for i, j := 0, len(ctxMsgs)-1; i < j; i, j = i+1, j-1 {
						ctxMsgs[i], ctxMsgs[j] = ctxMsgs[j], ctxMsgs[i]
					}
					msgs = append(msgs, ctxMsgs...)
				}
			}

			// Fetch the target message
			targetMsg, err := client.GetMessage(p.channelID, p.messageTs)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			msgs = append(msgs, *targetMsg)
		}

		if jsonOutput {
			jsonMsgs := format.MessagesToJSON(msgs, client.ResolveUser)
			payload := map[string]interface{}{
				"ok":       true,
				"channel":  channelName,
				"messages": jsonMsgs,
				"url":      rawURL,
			}
			if p.threadTs != "" {
				payload["thread_ts"] = p.threadTs
			}
			out, err := format.FormatJSON(payload)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(out)
			return
		}

		fmt.Print(format.FormatMessages(msgs, channelName, client.ResolveUser))
	},
}

func init() {
	openCmd.Flags().IntVar(&contextBefore, "context", 2, "Number of messages before the target for context")
	rootCmd.AddCommand(openCmd)
}
