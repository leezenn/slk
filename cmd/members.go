package cmd

import (
	"fmt"

	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

func newMembersCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "members <channel>",
		Short: "List channel members",
		Long:  "List members of a Slack channel, group, or DM conversation.",
		Example: `  slk members general          # Members of #general
  slk members C0123456789      # Members by channel ID
  slk members --json general   # Output as JSON`,
		Args: argumentValidator(cobra.ExactArgs(1)),
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: user cache unavailable: %v\n", err)
		}
		channelID, _, err := resolveTarget(client, args[0])
		if err != nil {
			return slackAPIError(err)
		}
		memberIDs, err := client.GetMembers(channelID)
		if err != nil {
			return slackAPIError(err)
		}

		if rootOptions.json {
			type memberJSON struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			members := make([]memberJSON, len(memberIDs))
			for i, id := range memberIDs {
				members[i] = memberJSON{ID: id, Name: client.ResolveUser(id)}
			}
			out, err := format.FormatJSON(members)
			if err != nil {
				return internalError()
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		}
		for _, id := range memberIDs {
			fmt.Fprintln(cmd.OutOrStdout(), client.ResolveUser(id))
		}
		return nil
	}
	return command
}
