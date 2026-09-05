package cmd

import (
	"fmt"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/leezenn/slk/internal/presentation"
	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

type replaceOptions struct {
	text         string
	presentation string
}

type replaceClient interface {
	messageOwnershipClient
	UpdateMessage(request api.UpdateMessageRequest) (*api.UpdateMessageResult, error)
}

func newReplaceCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &replaceOptions{}
	command := &cobra.Command{
		Use:   "replace <slack-permalink>",
		Short: "Replace the complete body of one Slack message",
		Long: `Replace the complete text of one top-level message or thread reply.

This is a whole-message replacement, not a patch or search-and-replace operation.
The exact message must be authored by the authenticated user. The command acts
immediately and applies the configured message_prefix to the replacement. Confirm
the exact permalink and complete replacement text before invoking it.` + presentationHelp() + formattingHelp("The --text value"),
		Example: `  slk replace 'https://workspace.slack.com/archives/C12345/p1705312325000100' --text 'The deployment completed successfully.'`,
		Args:    argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().StringVar(&options.text, "text", "", "Complete replacement message text")
	addPresentationFlag(command, &options.presentation)
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(options.text) == "" {
			return invalidArgument(cmd, "--text must contain the complete replacement message")
		}
		target, err := parseMessageMutationTarget(args[0])
		if err != nil {
			return invalidArgument(cmd, err.Error())
		}
		if _, err := resolvePresentation(cmd, options.presentation, presentation.Default()); err != nil {
			return err
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}
		bound, settings, err := bindCommandIdentity(cmd, deps)
		if err != nil {
			return err
		}
		mode, _ := resolvePresentation(cmd, options.presentation, settings.MessagePresentation)
		client, err := getClient(cmd, bound)
		if err != nil {
			return err
		}
		return runReplace(cmd, rootOptions, client, target, options.text, settings.MessagePrefix, mode, settings.Formatting...)
	}
	return command
}

func runReplace(cmd *cobra.Command, rootOptions *rootOptions, client replaceClient, target messageMutationTarget, text, prefix string, mode presentation.Mode, modules ...textformat.Module) error {
	if _, err := ownedMessageForMutation(client, target, mutationKindReplace); err != nil {
		return err
	}
	formatted := textformat.Apply(text, modules)
	_, err := client.UpdateMessage(api.UpdateMessageRequest{
		ChannelID:    target.channelID,
		MessageTs:    target.messageTs,
		Text:         formatted.Text,
		Prefix:       prefix,
		Presentation: mode,
	})
	if err != nil {
		return messageMutationError(err, mutationKindReplace)
	}

	if rootOptions.json {
		return writeJSON(cmd, map[string]interface{}{
			"ok":                   true,
			"replaced":             true,
			"target_permalink":     target.permalink,
			"open_command":         format.OpenCommand(target.permalink),
			"formatting_applied":   formattingReceipt(formatted.Applied),
			"message_presentation": mode,
		})
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Message replaced.")
	writeFormattingNotice(cmd, formatted.Applied)
	writeRequestedPresentation(cmd, mode)
	fmt.Fprintln(cmd.OutOrStdout(), target.permalink)
	fmt.Fprintf(cmd.OutOrStdout(), "Open: %s\n", format.OpenCommand(target.permalink))
	return nil
}
