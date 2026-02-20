package cmd

import (
	"fmt"
	"os"
	"regexp"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var fromAtRe = regexp.MustCompile(`from:@(\S+)`)

var searchLimit int

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search messages across workspace",
	Long: `Search for messages across all channels in the workspace.

Note: This command requires a user token (xoxp-), not a bot token.
Slack's search.messages API is only available with user tokens.`,
	Example: `  slk search "deploy failed"
  slk search "from:@john database" --limit 20
  slk search "in:#general bug report"`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		query := args[0]

		result, err := auth.GetToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		client := api.NewClient(result.Token)
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: user cache unavailable, from:@ resolution disabled: %v\n", err)
		}

		// Resolve from:@DisplayName to from:@username for Slack search API
		query = fromAtRe.ReplaceAllStringFunc(query, func(match string) string {
			name := fromAtRe.FindStringSubmatch(match)[1]
			resolved := client.ResolveDisplayNameToUsername(name)
			return "from:@" + resolved
		})

		searchResult, err := client.SearchMessages(query, searchLimit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if jsonOutput {
			out, err := format.FormatJSON(map[string]interface{}{
				"ok":      true,
				"total":   searchResult.Messages.Total,
				"matches": searchResult.Messages.Matches,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(out)
			return
		}

		fmt.Print(format.FormatSearchResults(searchResult, client.ResolveUser))
	},
}

func init() {
	searchCmd.Flags().IntVar(&searchLimit, "limit", 10, "Maximum number of search results")
	rootCmd.AddCommand(searchCmd)
}
