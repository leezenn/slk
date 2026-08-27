package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

type editOptions struct {
	match       string
	replacement string
}

type editClient interface {
	messageOwnershipClient
	UpdateMessage(request api.UpdateMessageRequest) (*api.UpdateMessageResult, error)
}

type editableMessageContent struct {
	body   string
	prefix string
}

type editableBlock struct {
	Type     string            `json:"type"`
	BlockID  string            `json:"block_id,omitempty"`
	Text     *editableText     `json:"text,omitempty"`
	Elements []json.RawMessage `json:"elements,omitempty"`
}

type editableText struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Verbatim *bool  `json:"verbatim,omitempty"`
}

func newEditCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &editOptions{}
	command := &cobra.Command{
		Use:   "edit <slack-permalink>",
		Short: "Edit one exact fragment of a Slack message",
		Long: `Replace one exact fragment within a self-authored Slack message body.

This command is deterministic and strictly non-interactive. --match must occur
exactly once in the current semantic body; zero matches fail as stale and multiple
matches fail as ambiguous. --with must be supplied and may be explicitly empty to
remove the fragment. Existing slk message prefixes and attachments are preserved.
Unsupported custom block layouts are refused; use 'slk replace' for those messages.`,
		Example: `  slk edit '<slack-permalink>' --match 'deploy tomorow' --with 'deploy tomorrow'
  slk edit '<slack-permalink>' --match 'obsolete sentence' --with ''`,
		Args: argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().StringVar(&options.match, "match", "", "Exact current body fragment (must occur once)")
	command.Flags().StringVar(&options.replacement, "with", "", "Replacement fragment; explicitly empty removes the match")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("match") || options.match == "" {
			return invalidArgument(cmd, "--match must contain the exact current fragment")
		}
		if !cmd.Flags().Changed("with") {
			return invalidArgument(cmd, "--with must be supplied; use --with '' to remove the match")
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
		return runEdit(cmd, rootOptions, client, target, options.match, options.replacement)
	}
	return command
}

func runEdit(cmd *cobra.Command, rootOptions *rootOptions, client editClient, target messageMutationTarget, match, replacement string) error {
	message, err := ownedMessageForMutation(client, target, mutationKindEdit)
	if err != nil {
		return err
	}
	content, err := decodeEditableMessageContent(message)
	if err != nil {
		return unsupportedEditLayoutError()
	}

	occurrences := overlappingOccurrenceCount(content.body, match)
	switch {
	case occurrences == 0:
		return newCommandError(
			ErrorConflict,
			"The exact edit match was not found in the current message body.",
			"Fresh-read the message and retry with an exact current fragment.",
		)
	case occurrences > 1:
		return newCommandError(
			ErrorConflict,
			fmt.Sprintf("The exact edit match occurs %d times and is ambiguous.", occurrences),
			"Use a longer fragment that occurs exactly once.",
		)
	}

	patched := strings.Replace(content.body, match, replacement, 1)
	if patched == content.body {
		return newCommandError(
			ErrorConflict,
			"The requested edit would not change the message body.",
			"Choose a replacement that changes the matched fragment.",
		)
	}
	if _, err := client.UpdateMessage(api.UpdateMessageRequest{
		ChannelID: target.channelID,
		MessageTs: target.messageTs,
		Text:      patched,
		Prefix:    content.prefix,
	}); err != nil {
		return messageMutationError(err, mutationKindEdit)
	}

	verified, err := messageForMutation(client, target, mutationKindEdit)
	if err != nil {
		return editVerificationError()
	}
	verifiedContent, err := decodeEditableMessageContent(verified)
	if err != nil || verifiedContent.body != patched || verifiedContent.prefix != content.prefix {
		return editVerificationError()
	}

	if rootOptions.json {
		return writeJSON(cmd, map[string]interface{}{
			"ok":               true,
			"edited":           true,
			"operation":        "replace_exact",
			"target_permalink": target.permalink,
			"open_command":     format.OpenCommand(target.permalink),
		})
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Message edited.")
	fmt.Fprintln(cmd.OutOrStdout(), target.permalink)
	fmt.Fprintf(cmd.OutOrStdout(), "Open: %s\n", format.OpenCommand(target.permalink))
	return nil
}

func decodeEditableMessageContent(message *api.Message) (editableMessageContent, error) {
	if len(message.Blocks) == 0 || allBlocksHaveType(message.Blocks, "rich_text") {
		return editableMessageContent{body: message.Text}, nil
	}
	if len(message.Blocks) < 2 {
		return editableMessageContent{}, fmt.Errorf("unsupported block layout")
	}

	contextBlock, err := decodeEditableBlock(message.Blocks[0], "type", "block_id", "elements")
	if err != nil || contextBlock.Type != "context" || len(contextBlock.Elements) != 1 {
		return editableMessageContent{}, fmt.Errorf("unsupported context block")
	}
	prefix, err := decodeEditableText(contextBlock.Elements[0])
	if err != nil || prefix.Type != "mrkdwn" || prefix.Text == "" {
		return editableMessageContent{}, fmt.Errorf("unsupported context element")
	}

	var body strings.Builder
	for _, rawBlock := range message.Blocks[1:] {
		section, err := decodeEditableBlock(rawBlock, "type", "block_id", "text")
		if err != nil || section.Type != "section" || section.Text == nil || section.Text.Type != "mrkdwn" {
			return editableMessageContent{}, fmt.Errorf("unsupported section block")
		}
		body.WriteString(section.Text.Text)
	}
	// Slack normalizes fallback whitespace when returning block messages. The exact
	// recognized blocks are the rendered content and therefore authoritative.
	return editableMessageContent{body: body.String(), prefix: prefix.Text}, nil
}

func allBlocksHaveType(blocks []json.RawMessage, blockType string) bool {
	for _, rawBlock := range blocks {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawBlock, &header); err != nil || header.Type != blockType {
			return false
		}
	}
	return len(blocks) > 0
}

func decodeEditableBlock(raw json.RawMessage, allowedKeys ...string) (editableBlock, error) {
	if err := requireObjectKeys(raw, allowedKeys...); err != nil {
		return editableBlock{}, err
	}
	var block editableBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return editableBlock{}, err
	}
	return block, nil
}

func decodeEditableText(raw json.RawMessage) (editableText, error) {
	if err := requireObjectKeys(raw, "type", "text", "verbatim"); err != nil {
		return editableText{}, err
	}
	var text editableText
	if err := json.Unmarshal(raw, &text); err != nil {
		return editableText{}, err
	}
	return text, nil
}

func requireObjectKeys(raw json.RawMessage, allowedKeys ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unsupported field %q", key)
		}
	}
	return nil
}

func overlappingOccurrenceCount(body, match string) int {
	count := 0
	for offset := 0; offset <= len(body)-len(match); {
		index := strings.Index(body[offset:], match)
		if index < 0 {
			break
		}
		count++
		absolute := offset + index
		_, size := utf8.DecodeRuneInString(body[absolute:])
		if size == 0 {
			break
		}
		offset = absolute + size
	}
	return count
}

func unsupportedEditLayoutError() error {
	return refusedError(
		"slk cannot safely edit this message's structured block layout.",
		"Use 'slk replace' for an intentional complete-body replacement.",
	)
}

func editVerificationError() error {
	return newCommandError(
		ErrorSlackAPI,
		"Slack accepted the edit, but slk could not verify the expected message body.",
		"Open the permalink and inspect the exact message before taking another action.",
	)
}
