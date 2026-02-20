package cmd

import (
	"fmt"
	"os"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var channelType string

var channelsCmd = &cobra.Command{
	Use:   "channels",
	Short: "List channels and conversations",
	Long:  "List Slack channels, DMs, group DMs, and private channels in your workspace.",
	Example: `  slk channels                  # List all channels
  slk channels --type public    # Public channels only
  slk channels --type dm        # Direct messages only
  slk channels --type private   # Private channels only
  slk channels --json           # Output as JSON`,
	Run: func(cmd *cobra.Command, args []string) {
		result, err := auth.GetToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		client := api.NewClient(result.Token)

		types := mapChannelType(channelType)

		channels, err := client.ListChannels(types, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Build user cache for DM name resolution
		var resolveUser func(string) string
		hasDMs := false
		for _, ch := range channels {
			if ch.IsIM {
				hasDMs = true
				break
			}
		}
		if hasDMs {
			if err := client.BuildUserCache(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: user cache unavailable: %v\n", err)
			}
		}
		resolveUser = client.ResolveUser

		if jsonOutput {
			out, err := format.FormatJSON(map[string]interface{}{
				"ok":       true,
				"channels": channels,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(out)
			return
		}

		fmt.Print(format.FormatChannels(channels, resolveUser))
	},
}

func mapChannelType(t string) string {
	switch t {
	case "public":
		return "public_channel"
	case "private":
		return "private_channel"
	case "dm":
		return "im"
	case "mpim":
		return "mpim"
	case "all", "":
		return "public_channel,private_channel,mpim,im"
	default:
		return "public_channel,private_channel,mpim,im"
	}
}

func init() {
	channelsCmd.Flags().StringVar(&channelType, "type", "all", "Channel type: all, public, private, dm, mpim")
	rootCmd.AddCommand(channelsCmd)
}
