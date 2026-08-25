package cmd

import (
	"fmt"
	"regexp"

	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var fromAtRe = regexp.MustCompile(`from:@(\S+)`)

type searchOptions struct {
	limit int
}

func newSearchCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &searchOptions{limit: 10}
	command := &cobra.Command{
		Use:   "search <query>",
		Short: "Search messages across workspace",
		Long: `Search for messages across all channels in the workspace.

Note: This command requires a user token (xoxp-), not a bot token.
Slack's search.messages API is only available with user tokens.`,
		Example: `  slk search "deploy failed"
  slk search "from:@john database" --limit 20
  slk search "in:#general bug report"`,
		Args: argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().IntVar(&options.limit, "limit", 10, "Maximum number of search results")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		selfID, err := identifySelf(client)
		if err != nil {
			return slackAPIError(err)
		}
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: user cache unavailable, from:@ resolution disabled: %v\n", err)
		}
		query := fromAtRe.ReplaceAllStringFunc(args[0], func(match string) string {
			name := fromAtRe.FindStringSubmatch(match)[1]
			return "from:@" + client.ResolveDisplayNameToUsername(name)
		})
		result, err := client.SearchMessages(query, options.limit)
		if err != nil {
			return slackAPIError(err)
		}
		if rootOptions.json {
			out, err := format.FormatJSON(map[string]interface{}{
				"ok": true, "total": result.Messages.Total,
				"matches": format.SearchMatchesToJSON(result.Messages.Matches, selfID),
			})
			if err != nil {
				return internalError()
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), format.FormatSearchResults(result, client.ResolveUser, selfID))
		return nil
	}
	return command
}
