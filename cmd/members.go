package cmd

import (
	"fmt"
	"os"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var membersCmd = &cobra.Command{
	Use:   "members <channel>",
	Short: "List channel members",
	Long:  "List members of a Slack channel, group, or DM conversation.",
	Example: `  slk members general          # Members of #general
  slk members C0123456789      # Members by channel ID
  slk members --json general   # Output as JSON`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]

		result, err := auth.GetToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		client := api.NewClient(result.Token)

		// Build user cache for name resolution
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: user cache unavailable: %v\n", err)
		}

		// Resolve target to channel ID
		channelID, _, err := resolveTarget(client, target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		memberIDs, err := client.GetMembers(channelID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if jsonOutput {
			type memberJSON struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			members := make([]memberJSON, len(memberIDs))
			for i, id := range memberIDs {
				members[i] = memberJSON{
					ID:   id,
					Name: client.ResolveUser(id),
				}
			}
			out, err := format.FormatJSON(members)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(out)
			return
		}

		for _, id := range memberIDs {
			fmt.Println(client.ResolveUser(id))
		}
	},
}

func init() {
	rootCmd.AddCommand(membersCmd)
}
