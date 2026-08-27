package cmd

import (
	"fmt"

	"github.com/leezenn/slk/internal/api"
	"github.com/spf13/cobra"
)

type deleteOptions struct {
	yes bool
}

type deleteClient interface {
	messageOwnershipClient
	DeleteMessage(channelID, messageTs string) (*api.DeleteMessageResult, error)
}

func newDeleteCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &deleteOptions{}
	command := &cobra.Command{
		Use:   "delete <slack-permalink>",
		Short: "Permanently delete one Slack message",
		Long: `Permanently delete one exact top-level message or thread reply.

The message must be authored by the authenticated user. Deleting a thread parent
does not delete its replies. This command is strictly non-interactive: it never
prompts or reads stdin, and it refuses to act unless --yes is supplied. Confirm
the exact permalink before invoking it.`,
		Example: `  slk delete 'https://workspace.slack.com/archives/C12345/p1705312325000100' --yes`,
		Args:    argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().BoolVar(&options.yes, "yes", false, "Confirm permanent deletion without an interactive prompt")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if !options.yes {
			return refusedError(
				"slk refuses to delete a message unless --yes is supplied.",
				"Confirm the exact permalink, then rerun the command with --yes.",
			)
		}
		target, err := parseMessageMutationTarget(args[0])
		if err != nil {
			return invalidArgument(cmd, err.Error())
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		return runDelete(cmd, rootOptions, client, target)
	}
	return command
}

func runDelete(cmd *cobra.Command, rootOptions *rootOptions, client deleteClient, target messageMutationTarget) error {
	message, err := ownedMessageForMutation(client, target, mutationKindDelete)
	if err != nil {
		return err
	}
	if _, err := client.DeleteMessage(target.channelID, target.messageTs); err != nil {
		return messageMutationError(err, mutationKindDelete)
	}

	if rootOptions.json {
		return writeJSON(cmd, map[string]interface{}{
			"ok":                        true,
			"deleted":                   true,
			"target_permalink":          target.permalink,
			"reply_count_before_delete": message.ReplyCount,
		})
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Message deleted.")
	fmt.Fprintf(cmd.OutOrStdout(), "Target: %s\n", target.permalink)
	if message.ReplyCount > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Replies before deletion: %d (preserved by Slack)\n", message.ReplyCount)
	}
	return nil
}
