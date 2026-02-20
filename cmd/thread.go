package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var threadLimit int

var threadCmd = &cobra.Command{
	Use:   "thread <channel> <thread-ts>",
	Short: "Read thread replies",
	Long: `Read all replies in a Slack thread.

The channel can be a name or ID. The thread-ts is the timestamp of the
parent message (visible in Slack message URLs or from the read command).`,
	Example: `  slk thread general 1705312325.000100
  slk thread C12345 1705312325.000100 --limit 100`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		channel := args[0]
		threadTs := args[1]

		result, err := auth.GetToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		client := api.NewClient(result.Token)
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: user cache unavailable: %v\n", err)
		}

		// Resolve channel
		channelID, channelName, err := resolveChannel(client, channel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		msgs, err := client.GetReplies(channelID, threadTs, threadLimit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if jsonOutput {
			jsonMsgs := format.MessagesToJSON(msgs, client.ResolveUser)
			out, err := format.FormatJSON(map[string]interface{}{
				"ok":        true,
				"channel":   channelName,
				"thread_ts": threadTs,
				"messages":  jsonMsgs,
			})
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

func resolveChannel(client *api.Client, channel string) (string, string, error) {
	// @username -> DM (reuse read's resolveTarget)
	if strings.HasPrefix(channel, "@") {
		return resolveTarget(client, channel)
	}
	// Looks like a channel ID
	if len(channel) >= 9 && (channel[0] == 'C' || channel[0] == 'G' || channel[0] == 'D') {
		return channel, channel, nil
	}
	ch, err := client.FindChannelByName(channel)
	if err != nil {
		return "", "", err
	}
	return ch.ID, ch.Name, nil
}

func init() {
	threadCmd.Flags().IntVar(&threadLimit, "limit", 50, "Maximum number of replies to retrieve")
	rootCmd.AddCommand(threadCmd)
}
