package cmd

import (
	"fmt"

	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

type channelsOptions struct {
	channelType string
}

func newChannelsCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &channelsOptions{channelType: "all"}
	command := &cobra.Command{
		Use:   "channels",
		Short: "List channels and conversations",
		Long:  "List Slack channels, DMs, group DMs, and private channels in your workspace.",
		Example: `  slk channels                  # List all channels
  slk channels --type public    # Public channels only
  slk channels --type dm        # Direct messages only
  slk channels --type private   # Private channels only
  slk channels --json           # Output as JSON`,
		Args: argumentValidator(cobra.NoArgs),
	}
	command.Flags().StringVar(&options.channelType, "type", "all", "Channel type: all, public, private, dm, mpim")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		channels, err := client.ListChannels(mapChannelType(options.channelType), 0)
		if err != nil {
			return slackAPIError(err)
		}

		hasDMs := false
		for _, channel := range channels {
			if channel.IsIM {
				hasDMs = true
				break
			}
		}
		if hasDMs {
			if err := client.BuildUserCache(); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: user cache unavailable: %v\n", err)
			}
		}

		if rootOptions.json {
			out, err := format.FormatJSON(map[string]interface{}{"ok": true, "channels": channels})
			if err != nil {
				return internalError()
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), format.FormatChannels(channels, client.ResolveUser))
		return nil
	}
	return command
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
