package cmd

import (
	"fmt"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

type whoamiIdentity struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	UserID      string `json:"user_id"`
	Workspace   string `json:"workspace"`
	WorkspaceID string `json:"workspace_id"`
}

type whoamiClient interface {
	AuthTest() (*api.AuthTestResult, error)
	GetUserInfo(userID string) (*api.User, error)
}

func newWhoamiCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "whoami",
		Short: "Show the authenticated Slack user",
		Long: `Show the Slack handle, display name, user ID, and workspace associated
with the configured token. Use this before interpreting message output to identify
which message author is you.`,
		Example: `  slk whoami
  slk whoami --json`,
		Args: argumentValidator(cobra.NoArgs),
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		identity, err := fetchWhoami(client)
		if err != nil {
			return slackAPIError(err)
		}
		if rootOptions.json {
			out, err := format.FormatJSON(map[string]interface{}{"ok": true, "identity": identity})
			if err != nil {
				return internalError()
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), formatWhoami(identity))
		return nil
	}
	return command
}

func fetchWhoami(client whoamiClient) (whoamiIdentity, error) {
	authResult, err := client.AuthTest()
	if err != nil {
		return whoamiIdentity{}, fmt.Errorf("identifying authenticated Slack user: %w", err)
	}
	if authResult.UserID == "" {
		return whoamiIdentity{}, fmt.Errorf("identifying authenticated Slack user: Slack returned an empty user ID")
	}
	user, err := client.GetUserInfo(authResult.UserID)
	if err != nil {
		return whoamiIdentity{}, fmt.Errorf("fetching authenticated Slack profile: %w", err)
	}
	handle := strings.TrimPrefix(user.Name, "@")
	if handle == "" {
		handle = strings.TrimPrefix(authResult.User, "@")
	}
	displayName := user.Profile.DisplayName
	if displayName == "" {
		displayName = handle
	}
	return whoamiIdentity{
		Handle: handle, DisplayName: displayName, UserID: authResult.UserID,
		Workspace: authResult.Team, WorkspaceID: authResult.TeamID,
	}, nil
}

func formatWhoami(identity whoamiIdentity) string {
	return fmt.Sprintf(
		"Authenticated Slack user:\n  Handle:       @%s\n  Display name: %s\n  User ID:      %s\n  Workspace:    %s (%s)\n",
		identity.Handle, identity.DisplayName, identity.UserID, identity.Workspace, identity.WorkspaceID,
	)
}
