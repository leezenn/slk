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

var userStatus bool

var usersCmd = &cobra.Command{
	Use:   "users [query]",
	Short: "List workspace users",
	Long: `List all users in the Slack workspace.

Optionally filter by name or display name with a search query.`,
	Example: `  slk users                # List all users
  slk users john           # Filter users matching "john"
  slk users john --status  # Show online/away (1 API call per user)
  slk users --json         # Output as JSON`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		result, err := auth.GetToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		client := api.NewClient(result.Token)

		users, err := client.ListUsers()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// Filter if query provided
		if len(args) == 1 {
			query := strings.ToLower(args[0])
			var filtered []api.User
			for _, u := range users {
				name := strings.ToLower(u.Name)
				displayName := strings.ToLower(u.Profile.DisplayName)
				realName := strings.ToLower(u.RealName)
				if strings.Contains(name, query) ||
					strings.Contains(displayName, query) ||
					strings.Contains(realName, query) {
					filtered = append(filtered, u)
				}
			}
			users = filtered
		}

		// Fetch per-user presence when --status is set
		if userStatus {
			for i, u := range users {
				if u.Deleted || u.IsBot {
					continue
				}
				presence, err := client.GetPresence(u.ID)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: presence for %s: %v\n", u.Name, err)
					continue
				}
				users[i].Presence = presence
			}
		}

		if jsonOutput {
			out, err := format.FormatJSON(map[string]interface{}{
				"ok":    true,
				"users": users,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(out)
			return
		}

		fmt.Print(format.FormatUsers(users))
	},
}

func init() {
	rootCmd.AddCommand(usersCmd)
	usersCmd.Flags().BoolVar(&userStatus, "status", false, "Show online/away presence (1 API call per user, use with a query)")
}
