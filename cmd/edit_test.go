package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
	"github.com/spf13/cobra"
)

type fakeEditClient struct {
	selfID        string
	messages      []*api.Message
	messageIndex  int
	updateRequest api.UpdateMessageRequest
	updateResult  *api.UpdateMessageResult
	updateErr     error
	updateCalls   int
}

func (f *fakeEditClient) Identify() error { return nil }
func (f *fakeEditClient) SelfID() string  { return f.selfID }

func (f *fakeEditClient) GetMessage(string, string) (*api.Message, error) {
	return f.nextMessage()
}

func (f *fakeEditClient) GetReply(string, string, string) (*api.Message, error) {
	return f.nextMessage()
}

func (f *fakeEditClient) nextMessage() (*api.Message, error) {
	if len(f.messages) == 0 {
		return nil, errors.New("message unavailable")
	}
	index := f.messageIndex
	if index >= len(f.messages) {
		index = len(f.messages) - 1
	}
	f.messageIndex++
	return f.messages[index], nil
}

func (f *fakeEditClient) UpdateMessage(request api.UpdateMessageRequest) (*api.UpdateMessageResult, error) {
	f.updateCalls++
	f.updateRequest = request
	return f.updateResult, f.updateErr
}

func TestRunEditPatchesPlainMessageAndVerifies(t *testing.T) {
	target := rootMessageTarget()
	client := &fakeEditClient{
		selfID: "U12345678",
		messages: []*api.Message{
			{User: "U12345678", Ts: target.messageTs, Text: "deploy tomorow now"},
			{User: "U12345678", Ts: target.messageTs, Text: "deploy tomorrow now"},
		},
		updateResult: &api.UpdateMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)

	if err := runEdit(command, &rootOptions{json: true}, client, target, "tomorow", "tomorrow"); err != nil {
		t.Fatalf("runEdit() error = %v", err)
	}
	wantRequest := api.UpdateMessageRequest{
		ChannelID: target.channelID, MessageTs: target.messageTs,
		Text: "deploy tomorrow now",
	}
	if client.updateRequest != wantRequest || client.updateCalls != 1 || client.messageIndex != 2 {
		t.Fatalf("edit state = request %#v calls %d reads %d", client.updateRequest, client.updateCalls, client.messageIndex)
	}
	for _, want := range []string{`"edited": true`, `"operation": "replace_exact"`, `"target_permalink": "` + target.permalink + `"`, `"open_command": "slk open '`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("JSON receipt omitted %q: %s", want, stdout.String())
		}
	}
	for _, forbidden := range []string{`"channel":`, `"ts":`, `"before":`, `"after":`} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("edit JSON exposed %q: %s", forbidden, stdout.String())
		}
	}
}

func TestRunEditPreservesExistingPrefixAcrossSplitSections(t *testing.T) {
	target := rootMessageTarget()
	prefix := "agent assisted"
	currentBody := "first old line\nsecond section"
	patchedBody := "first new line\nsecond section"
	client := &fakeEditClient{
		selfID: "U12345678",
		messages: []*api.Message{
			prefixedMessage(t, "U12345678", target.messageTs, prefix, "first old line\n", "second section"),
			prefixedMessage(t, "U12345678", target.messageTs, prefix, patchedBody),
		},
		updateResult: &api.UpdateMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)

	if err := runEdit(command, &rootOptions{}, client, target, "old", "new"); err != nil {
		t.Fatalf("runEdit() error = %v", err)
	}
	if client.updateRequest.Text != patchedBody || client.updateRequest.Prefix != prefix {
		t.Fatalf("update request = %#v; current body %q", client.updateRequest, currentBody)
	}
	if !strings.Contains(stdout.String(), "Message edited.") || !strings.Contains(stdout.String(), target.permalink) {
		t.Fatalf("text receipt = %q", stdout.String())
	}
}

func TestDecodeEditableMessageContentAcceptsSlackReturnedMetadataAndNormalizedFallback(t *testing.T) {
	prefix := "agent assisted"
	body := "deploy tomorow"
	message := &api.Message{
		User: "U12345678",
		Ts:   "1705312325.000100",
		Text: prefix + "  " + body,
		Blocks: []json.RawMessage{
			rawJSON(t, map[string]interface{}{
				"type":     "context",
				"block_id": "context-id",
				"elements": []interface{}{map[string]interface{}{
					"type": "mrkdwn", "text": prefix, "verbatim": false,
				}},
			}),
			rawJSON(t, map[string]interface{}{
				"type":     "section",
				"block_id": "section-id",
				"text": map[string]interface{}{
					"type": "mrkdwn", "text": body, "verbatim": false,
				},
			}),
		},
	}

	content, err := decodeEditableMessageContent(message)
	if err != nil {
		t.Fatalf("decodeEditableMessageContent() error = %v", err)
	}
	if content.prefix != prefix || content.body != body {
		t.Fatalf("decoded content = %#v", content)
	}
}

func TestDecodeEditableMessageContentRejectsMalformedSlackMetadata(t *testing.T) {
	tests := []struct {
		name   string
		blocks []json.RawMessage
	}{
		{
			name: "block id must be string",
			blocks: []json.RawMessage{
				rawJSON(t, map[string]interface{}{
					"type": "context", "block_id": 42,
					"elements": []interface{}{map[string]interface{}{"type": "mrkdwn", "text": "prefix"}},
				}),
				rawJSON(t, map[string]interface{}{
					"type": "section", "text": map[string]interface{}{"type": "mrkdwn", "text": "body"},
				}),
			},
		},
		{
			name: "verbatim must be boolean",
			blocks: []json.RawMessage{
				rawJSON(t, map[string]interface{}{
					"type": "context",
					"elements": []interface{}{map[string]interface{}{
						"type": "mrkdwn", "text": "prefix", "verbatim": "false",
					}},
				}),
				rawJSON(t, map[string]interface{}{
					"type": "section", "text": map[string]interface{}{"type": "mrkdwn", "text": "body"},
				}),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeEditableMessageContent(&api.Message{Blocks: test.blocks})
			if err == nil {
				t.Fatal("decodeEditableMessageContent() unexpectedly accepted malformed metadata")
			}
		})
	}
}

func TestRunEditAcceptsExplicitEmptyReplacement(t *testing.T) {
	target := rootMessageTarget()
	client := &fakeEditClient{
		selfID: "U12345678",
		messages: []*api.Message{
			{User: "U12345678", Ts: target.messageTs, Text: "remove obsolete sentence"},
			{User: "U12345678", Ts: target.messageTs, Text: "remove sentence"},
		},
		updateResult: &api.UpdateMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	if err := runEdit(&cobra.Command{}, &rootOptions{}, client, target, "obsolete ", ""); err != nil {
		t.Fatalf("runEdit() error = %v", err)
	}
	if client.updateRequest.Text != "remove sentence" {
		t.Fatalf("edited body = %q", client.updateRequest.Text)
	}
}

func TestRunEditFailsClosedForMissingAmbiguousAndNoOpMatches(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		match       string
		replacement string
		want        string
	}{
		{name: "missing", body: "current body", match: "stale", replacement: "new", want: "not found"},
		{name: "repeated", body: "old and old", match: "old", replacement: "new", want: "2 times"},
		{name: "overlapping", body: "aaaa", match: "aa", replacement: "b", want: "3 times"},
		{name: "no-op", body: "current body", match: "current", replacement: "current", want: "would not change"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := rootMessageTarget()
			client := &fakeEditClient{
				selfID:   "U12345678",
				messages: []*api.Message{{User: "U12345678", Ts: target.messageTs, Text: test.body}},
			}
			err := runEdit(&cobra.Command{}, &rootOptions{}, client, target, test.match, test.replacement)
			var commandErr *CommandError
			if !errors.As(err, &commandErr) || commandErr.Code != ErrorConflict || !strings.Contains(commandErr.Message, test.want) {
				t.Fatalf("error = %#v", err)
			}
			if client.updateCalls != 0 {
				t.Fatalf("unsafe edit made %d update calls", client.updateCalls)
			}
		})
	}
}

func TestRunEditSupportsSlackRichTextAsCanonicalPlainBody(t *testing.T) {
	target := rootMessageTarget()
	richBlock := rawJSON(t, map[string]interface{}{
		"type":     "rich_text",
		"elements": []interface{}{map[string]interface{}{"type": "rich_text_section"}},
	})
	client := &fakeEditClient{
		selfID: "U12345678",
		messages: []*api.Message{
			{User: "U12345678", Ts: target.messageTs, Text: "old body", Blocks: []json.RawMessage{richBlock}},
			{User: "U12345678", Ts: target.messageTs, Text: "new body", Blocks: []json.RawMessage{richBlock}},
		},
		updateResult: &api.UpdateMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	if err := runEdit(&cobra.Command{}, &rootOptions{}, client, target, "old", "new"); err != nil {
		t.Fatalf("runEdit() error = %v", err)
	}
	if client.updateRequest.Text != "new body" || client.updateRequest.Prefix != "" {
		t.Fatalf("rich-text update request = %#v", client.updateRequest)
	}
}

func TestRunEditRefusesUnsupportedCustomBlocks(t *testing.T) {
	target := rootMessageTarget()
	custom := rawJSON(t, map[string]interface{}{
		"type":      "section",
		"text":      map[string]interface{}{"type": "mrkdwn", "text": "old body"},
		"accessory": map[string]interface{}{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Go"}},
	})
	client := &fakeEditClient{
		selfID:   "U12345678",
		messages: []*api.Message{{User: "U12345678", Ts: target.messageTs, Text: "old body", Blocks: []json.RawMessage{custom}}},
	}
	err := runEdit(&cobra.Command{}, &rootOptions{}, client, target, "old", "new")
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != ErrorRefused || !strings.Contains(commandErr.Action, "slk replace") {
		t.Fatalf("error = %#v", err)
	}
	if client.updateCalls != 0 {
		t.Fatalf("unsupported edit made %d update calls", client.updateCalls)
	}
}

func TestRunEditReportsPostUpdateVerificationMismatch(t *testing.T) {
	target := rootMessageTarget()
	client := &fakeEditClient{
		selfID: "U12345678",
		messages: []*api.Message{
			{User: "U12345678", Ts: target.messageTs, Text: "old body"},
			{User: "U12345678", Ts: target.messageTs, Text: "unexpected body"},
		},
		updateResult: &api.UpdateMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	err := runEdit(&cobra.Command{}, &rootOptions{}, client, target, "old", "new")
	var commandErr *CommandError
	if !errors.As(err, &commandErr) || commandErr.Code != ErrorSlackAPI || !strings.Contains(commandErr.Message, "could not verify") {
		t.Fatalf("error = %#v", err)
	}
	if client.updateCalls != 1 {
		t.Fatalf("update calls = %d", client.updateCalls)
	}
}

func TestEditFlagsAreNonInteractiveAndAcceptExplicitEmptyWith(t *testing.T) {
	permalink := rootMessageTarget().permalink

	store := &fakeCredentialStore{}
	deps := isolatedDependencies(store)
	var stdout, stderr bytes.Buffer
	code := execute(
		deps,
		context.Background(),
		[]string{"edit", permalink, "--match", "old"},
		forbiddenEditInput{t: t},
		&stdout,
		&stderr,
	)
	if code != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "--with must be supplied") || store.getCalls != 0 {
		t.Fatalf("missing with = code %d stdout %q stderr %q credentials %d", code, stdout.String(), stderr.String(), store.getCalls)
	}

	missingAuth := &fakeCredentialStore{getErr: errors.New("missing")}
	code, stdoutText, stderrText := runIsolated(
		t,
		isolatedDependencies(missingAuth),
		context.Background(),
		"edit", permalink, "--match", "old", "--with", "",
	)
	if code != 1 || stdoutText != "" || !strings.Contains(stderrText, "authentication is not configured") || strings.Contains(stderrText, "--with must be supplied") {
		t.Fatalf("explicit empty with = code %d stdout %q stderr %q", code, stdoutText, stderrText)
	}
}

type forbiddenEditInput struct{ t *testing.T }

func (r forbiddenEditInput) Read([]byte) (int, error) {
	r.t.Fatal("edit read stdin")
	return 0, io.EOF
}

func prefixedMessage(t *testing.T, userID, ts, prefix string, sections ...string) *api.Message {
	t.Helper()
	blocks := []json.RawMessage{rawJSON(t, map[string]interface{}{
		"type":     "context",
		"elements": []interface{}{map[string]string{"type": "mrkdwn", "text": prefix}},
	})}
	var body strings.Builder
	for _, section := range sections {
		body.WriteString(section)
		blocks = append(blocks, rawJSON(t, map[string]interface{}{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": section},
		}))
	}
	return &api.Message{
		User: userID, Ts: ts, Text: prefix + "\n\n" + body.String(), Blocks: blocks,
	}
}

func rawJSON(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

var _ editClient = (*fakeEditClient)(nil)
