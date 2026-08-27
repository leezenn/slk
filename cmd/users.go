package cmd

import (
	"fmt"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

type usersOptions struct {
	status bool
}

type userJSON struct {
	UserID      string `json:"user_id"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Deleted     bool   `json:"deleted"`
	IsBot       bool   `json:"is_bot"`
	Presence    string `json:"presence,omitempty"`
}

func newUsersCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &usersOptions{}
	command := &cobra.Command{
		Use:   "users [query]",
		Short: "List workspace users",
		Long: `List all users in the Slack workspace.

Optionally filter by name or display name with a search query.`,
		Example: `  slk users                # List all users
  slk users john           # Filter users matching "john"
  slk users john --status  # Show online/away (1 API call per user)
  slk users --json         # Output as JSON`,
		Args: argumentValidator(cobra.MaximumNArgs(1)),
	}
	command.Flags().BoolVar(&options.status, "status", false, "Show online/away presence (1 API call per user, use with a query)")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		users, err := client.ListUsers()
		if err != nil {
			return slackAPIError(err)
		}
		if len(args) == 1 {
			query := strings.ToLower(args[0])
			filtered := make([]api.User, 0, len(users))
			for _, user := range users {
				if strings.Contains(strings.ToLower(user.Name), query) ||
					strings.Contains(strings.ToLower(user.Profile.DisplayName), query) ||
					strings.Contains(strings.ToLower(user.RealName), query) {
					filtered = append(filtered, user)
				}
			}
			users = filtered
		}
		if options.status {
			for i, user := range users {
				if user.Deleted || user.IsBot {
					continue
				}
				presence, err := client.GetPresence(user.ID)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: presence for %s: %v\n", user.Name, err)
					continue
				}
				users[i].Presence = presence
			}
		}
		if rootOptions.json {
			return writeJSON(cmd, map[string]interface{}{"ok": true, "users": usersToJSON(users)})
		}
		fmt.Fprint(cmd.OutOrStdout(), format.FormatUsers(users))
		return nil
	}
	return command
}

func usersToJSON(users []api.User) []userJSON {
	projected := make([]userJSON, 0, len(users))
	for _, user := range users {
		handle := strings.TrimPrefix(strings.TrimSpace(user.Name), "@")
		displayName := firstNonEmpty(
			user.Profile.DisplayName,
			user.Profile.RealName,
			user.RealName,
			handle,
		)
		projected = append(projected, userJSON{
			UserID:      user.ID,
			Handle:      handle,
			DisplayName: displayName,
			Deleted:     user.Deleted,
			IsBot:       user.IsBot,
			Presence:    user.Presence,
		})
	}
	return projected
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
