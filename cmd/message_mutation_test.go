package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

type fakeMessageMutationClient struct {
	selfID         string
	identifyErr    error
	message        *api.Message
	messageErr     error
	reply          *api.Message
	replyErr       error
	getMessageCall bool
	getReplyCall   bool
	updateRequest  api.UpdateMessageRequest
	updateResult   *api.UpdateMessageResult
	updateErr      error
	updateCalls    int
	deleteChannel  string
	deleteTs       string
	deleteResult   *api.DeleteMessageResult
	deleteErr      error
	deleteCalls    int
}

func (f *fakeMessageMutationClient) Identify() error { return f.identifyErr }
func (f *fakeMessageMutationClient) SelfID() string  { return f.selfID }

func (f *fakeMessageMutationClient) GetMessage(string, string) (*api.Message, error) {
	f.getMessageCall = true
	return f.message, f.messageErr
}

func (f *fakeMessageMutationClient) GetReply(string, string, string) (*api.Message, error) {
	f.getReplyCall = true
	return f.reply, f.replyErr
}

func (f *fakeMessageMutationClient) UpdateMessage(request api.UpdateMessageRequest) (*api.UpdateMessageResult, error) {
	f.updateCalls++
	f.updateRequest = request
	return f.updateResult, f.updateErr
}

func (f *fakeMessageMutationClient) DeleteMessage(channelID, messageTs string) (*api.DeleteMessageResult, error) {
	f.deleteCalls++
	f.deleteChannel = channelID
	f.deleteTs = messageTs
	return f.deleteResult, f.deleteErr
}

func rootMessageTarget() messageMutationTarget {
	return messageMutationTarget{
		channelID: "C12345678",
		messageTs: "1700000001.000002",
		permalink: "https://example.slack.com/archives/C12345678/p1700000001000002",
	}
}

func TestParseMessageMutationTargetPreservesExactReply(t *testing.T) {
	tests := []struct {
		name       string
		permalink  string
		wantThread string
		wantErr    bool
	}{
		{
			name:      "root message",
			permalink: "<https://example.slack.com/archives/C12345678/p1700000001000002>",
		},
		{
			name:       "thread reply",
			permalink:  "https://example.slack.com/archives/C12345678/p1700000001000002?thread_ts=1700000000.000001&cid=C12345678",
			wantThread: "1700000000.000001",
		},
		{
			name:      "invalid thread",
			permalink: "https://example.slack.com/archives/C12345678/p1700000001000002?thread_ts=invalid",
			wantErr:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := parseMessageMutationTarget(test.permalink)
			if (err != nil) != test.wantErr {
				t.Fatalf("parse error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil {
				if target.channelID != "C12345678" || target.messageTs != "1700000001.000002" || target.threadTs != test.wantThread {
					t.Fatalf("target = %#v", target)
				}
				if strings.Contains(target.permalink, "<") || strings.Contains(target.permalink, ">") {
					t.Fatalf("target retained Slack wrapper: %q", target.permalink)
				}
			}
		})
	}
}

func TestRunReplaceUsesOwnedRootMessageAndReturnsReceipt(t *testing.T) {
	target := rootMessageTarget()
	client := &fakeMessageMutationClient{
		selfID:       "U12345678",
		message:      &api.Message{User: "U12345678", Ts: target.messageTs},
		updateResult: &api.UpdateMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)

	err := runReplace(command, &rootOptions{}, client, target, "Complete replacement.", "agent assisted")
	if err != nil {
		t.Fatalf("runReplace() error = %v", err)
	}
	wantRequest := api.UpdateMessageRequest{
		ChannelID: target.channelID, MessageTs: target.messageTs,
		Text: "Complete replacement.", Prefix: "agent assisted",
	}
	if client.updateRequest != wantRequest || client.updateCalls != 1 || !client.getMessageCall || client.getReplyCall {
		t.Fatalf("replace state = request %#v update %d root %v reply %v", client.updateRequest, client.updateCalls, client.getMessageCall, client.getReplyCall)
	}
	for _, want := range []string{"Message replaced.", target.permalink, "Open: slk open '" + target.permalink + "'"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("receipt omitted %q: %q", want, stdout.String())
		}
	}
}

func TestRunReplaceAppliesEnabledFormatting(t *testing.T) {
	target := rootMessageTarget()
	client := &fakeMessageMutationClient{
		selfID:       "U12345678",
		message:      &api.Message{User: "U12345678", Ts: target.messageTs},
		updateResult: &api.UpdateMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)

	if err := runReplace(
		command,
		&rootOptions{},
		client,
		target,
		"complete—replacement",
		"prefix",
		textformat.ModuleEmDashToSpacedHyphen,
	); err != nil {
		t.Fatalf("runReplace() error = %v", err)
	}
	if client.updateRequest.Text != "complete - replacement" {
		t.Fatalf("formatted replacement = %#v", client.updateRequest)
	}
	if !strings.Contains(stdout.String(), "Formatting applied: em-dash-to-spaced-hyphen.") {
		t.Fatalf("replace receipt omitted formatting: %q", stdout.String())
	}
}

func TestRunReplaceFetchesExactReplyAndReturnsJSON(t *testing.T) {
	target := rootMessageTarget()
	target.threadTs = "1700000000.000001"
	target.permalink += "?thread_ts=" + target.threadTs
	client := &fakeMessageMutationClient{
		selfID:       "U12345678",
		reply:        &api.Message{User: "U12345678", Ts: target.messageTs},
		updateResult: &api.UpdateMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)

	if err := runReplace(command, &rootOptions{json: true}, client, target, "replacement", ""); err != nil {
		t.Fatalf("runReplace() error = %v", err)
	}
	if client.getMessageCall || !client.getReplyCall {
		t.Fatalf("reply lookup = root %v reply %v", client.getMessageCall, client.getReplyCall)
	}
	for _, want := range []string{`"replaced": true`, `"target_permalink": "` + target.permalink + `"`, `"open_command": "slk open '`, `"formatting_applied": []`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("JSON receipt omitted %q: %s", want, stdout.String())
		}
	}
	for _, forbidden := range []string{`"channel":`, `"ts":`, `"permalink":`} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("replace JSON exposed %q: %s", forbidden, stdout.String())
		}
	}
}

func TestMessageMutationsRefuseOtherAuthorsBeforeMutation(t *testing.T) {
	target := rootMessageTarget()
	client := &fakeMessageMutationClient{
		selfID:  "U12345678",
		message: &api.Message{User: "U87654321", Ts: target.messageTs},
	}

	for _, test := range []struct {
		name string
		run  func(*cobra.Command) error
	}{
		{name: "replace", run: func(command *cobra.Command) error {
			return runReplace(command, &rootOptions{}, client, target, "replacement", "prefix")
		}},
		{name: "delete", run: func(command *cobra.Command) error {
			return runDelete(command, &rootOptions{}, client, target)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run(&cobra.Command{})
			var commandErr *CommandError
			if !errors.As(err, &commandErr) || commandErr.Code != ErrorRefused || !strings.Contains(commandErr.Message, "not authored") {
				t.Fatalf("error = %#v", err)
			}
		})
	}
	if client.updateCalls != 0 || client.deleteCalls != 0 {
		t.Fatalf("other-author mutation calls = update %d delete %d", client.updateCalls, client.deleteCalls)
	}
}

func TestRunDeleteReturnsPermanentReceiptAndPreservedReplyCount(t *testing.T) {
	target := rootMessageTarget()
	client := &fakeMessageMutationClient{
		selfID:       "U12345678",
		message:      &api.Message{User: "U12345678", Ts: target.messageTs, ReplyCount: 3},
		deleteResult: &api.DeleteMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)

	if err := runDelete(command, &rootOptions{}, client, target); err != nil {
		t.Fatalf("runDelete() error = %v", err)
	}
	if client.deleteChannel != target.channelID || client.deleteTs != target.messageTs || client.deleteCalls != 1 {
		t.Fatalf("delete target = %s %s calls %d", client.deleteChannel, client.deleteTs, client.deleteCalls)
	}
	for _, want := range []string{"Message deleted.", "Target: " + target.permalink, "Replies before deletion: 3 (preserved by Slack)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("receipt omitted %q: %q", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "Open:") {
		t.Fatalf("delete receipt included misleading open command: %q", stdout.String())
	}
}

func TestRunDeleteReturnsJSONWithoutOpenCommand(t *testing.T) {
	target := rootMessageTarget()
	client := &fakeMessageMutationClient{
		selfID:       "U12345678",
		message:      &api.Message{User: "U12345678", Ts: target.messageTs},
		deleteResult: &api.DeleteMessageResult{Channel: target.channelID, Ts: target.messageTs},
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)

	if err := runDelete(command, &rootOptions{json: true}, client, target); err != nil {
		t.Fatalf("runDelete() error = %v", err)
	}
	for _, want := range []string{`"deleted": true`, `"reply_count_before_delete": 0`, `"target_permalink": "` + target.permalink + `"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("JSON receipt omitted %q: %s", want, stdout.String())
		}
	}
	for _, forbidden := range []string{`"channel":`, `"ts":`, `"permalink":`, `"open_command":`} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("delete JSON exposed %q: %s", forbidden, stdout.String())
		}
	}
}

func TestMessageMutationErrorsGuideSafeRecovery(t *testing.T) {
	tests := []struct {
		name        string
		kind        messageMutationKind
		err         error
		wantCode    ErrorCode
		wantMessage string
		wantAction  string
	}{
		{name: "edit uncertain", kind: mutationKindEdit, err: errors.New("connection closed"), wantCode: ErrorSlackAPI, wantMessage: "message was edited", wantAction: "before retrying"},
		{name: "replace uncertain", kind: mutationKindReplace, err: errors.New("connection closed"), wantCode: ErrorSlackAPI, wantMessage: "did not confirm whether", wantAction: "before retrying"},
		{name: "delete uncertain", kind: mutationKindDelete, err: &api.MethodError{Code: "internal_error"}, wantCode: ErrorSlackAPI, wantMessage: "did not confirm whether", wantAction: "before retrying"},
		{name: "edit ownership", kind: mutationKindEdit, err: &api.MethodError{Code: "cant_update_message"}, wantCode: ErrorRefused, wantMessage: "does not allow", wantAction: "authored"},
		{name: "replace ownership", kind: mutationKindReplace, err: &api.MethodError{Code: "cant_update_message"}, wantCode: ErrorRefused, wantMessage: "does not allow", wantAction: "authored"},
		{name: "delete ownership", kind: mutationKindDelete, err: &api.MethodError{Code: "cant_delete_message"}, wantCode: ErrorRefused, wantMessage: "does not allow", wantAction: "authored"},
		{name: "edit window", kind: mutationKindReplace, err: &api.MethodError{Code: "edit_window_closed"}, wantCode: ErrorRefused, wantMessage: "window has closed", wantAction: "correction"},
		{name: "missing target", kind: mutationKindDelete, err: &api.MethodError{Code: "message_not_found"}, wantCode: ErrorSlackAPI, wantMessage: "could not find", wantAction: "permalink"},
		{name: "scope", kind: mutationKindDelete, err: &api.MethodError{Code: "missing_scope"}, wantCode: ErrorAuthFailed, wantMessage: "current credential", wantAction: "chat:write"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := messageMutationError(test.err, test.kind)
			var commandErr *CommandError
			if !errors.As(err, &commandErr) || commandErr.Code != test.wantCode || !strings.Contains(commandErr.Message, test.wantMessage) || !strings.Contains(commandErr.Action, test.wantAction) {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

type forbiddenInput struct{ t *testing.T }

func (r forbiddenInput) Read([]byte) (int, error) {
	r.t.Fatal("delete without --yes read stdin")
	return 0, io.EOF
}

func TestDeleteWithoutYesRefusesBeforeCredentialSlackOrStdin(t *testing.T) {
	store := &fakeCredentialStore{}
	deps := isolatedDependencies(store)
	var stdout, stderr bytes.Buffer
	code := execute(
		deps,
		context.Background(),
		[]string{"delete", rootMessageTarget().permalink},
		forbiddenInput{t: t},
		&stdout,
		&stderr,
	)
	if code != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "unless --yes is supplied") || strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("delete refusal = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if store.getCalls != 0 {
		t.Fatalf("delete without --yes read credentials %d times", store.getCalls)
	}
}
