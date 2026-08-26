package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
	"github.com/spf13/cobra"
)

type fakeReplyClient struct {
	posted       *api.PostMessageResult
	postErr      error
	permalink    string
	permalinkErr error
	channelID    string
	threadTs     string
	text         string
	prefix       string
}

func (f *fakeReplyClient) PostReply(channelID, threadTs, text, prefix string) (*api.PostMessageResult, error) {
	f.channelID, f.threadTs, f.text, f.prefix = channelID, threadTs, text, prefix
	return f.posted, f.postErr
}

func (f *fakeReplyClient) GetPermalink(string, string) (string, error) {
	return f.permalink, f.permalinkErr
}

func TestRunReplyReturnsCanonicalReceipt(t *testing.T) {
	const permalink = "https://example.slack.com/archives/C12345678/p1700000001000002?thread_ts=1700000000.000001&cid=C12345678"
	client := &fakeReplyClient{
		posted:    &api.PostMessageResult{Channel: "C12345678", Ts: "1700000001.000002"},
		permalink: permalink,
	}
	var stdout, stderr bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	target := replyTarget{channelID: "C12345678", threadTs: "1700000000.000001"}

	if err := runReply(command, &rootOptions{}, client, target, "We will ship the fix tomorrow.", ":mechanical_arm: agent assisted response."); err != nil {
		t.Fatalf("runReply returned error: %v", err)
	}
	if client.channelID != target.channelID || client.threadTs != target.threadTs || client.text != "We will ship the fix tomorrow." || client.prefix != ":mechanical_arm: agent assisted response." {
		t.Fatalf("posted arguments = channel %q thread %q text %q prefix %q", client.channelID, client.threadTs, client.text, client.prefix)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{"Reply posted.", permalink, "Open: slk open '" + permalink + "'"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("receipt omitted %q: %q", want, stdout.String())
		}
	}
}

func TestRunReplyReturnsJSONReceipt(t *testing.T) {
	const permalink = "https://example.slack.com/archives/C12345678/p1700000001000002"
	client := &fakeReplyClient{
		posted:    &api.PostMessageResult{Channel: "C12345678", Ts: "1700000001.000002"},
		permalink: permalink,
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)
	target := replyTarget{channelID: "C12345678", threadTs: "1700000000.000001"}

	if err := runReply(command, &rootOptions{json: true}, client, target, "hello", "prefix"); err != nil {
		t.Fatalf("runReply returned error: %v", err)
	}
	for _, want := range []string{`"posted": true`, `"permalink": "` + permalink + `"`, `"open_command": "slk open '` + permalink + `'"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("JSON receipt omitted %q: %s", want, stdout.String())
		}
	}
}

func TestRunReplyFallsBackWhenPermalinkLookupFails(t *testing.T) {
	client := &fakeReplyClient{
		posted:       &api.PostMessageResult{Channel: "C12345678", Ts: "1700000001.000002"},
		permalinkErr: errors.New("lookup unavailable"),
	}
	var stdout, stderr bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	target := replyTarget{channelID: "C12345678", threadTs: "1700000000.000001"}

	if err := runReply(command, &rootOptions{}, client, target, "hello", "prefix"); err != nil {
		t.Fatalf("runReply returned error: %v", err)
	}
	for _, want := range []string{"Reply posted.", "Channel: C12345678", "Timestamp: 1700000001.000002", "Thread: slk thread C12345678 1700000000.000001"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("fallback receipt omitted %q: %q", want, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "reply posted, but Slack could not return its permalink") {
		t.Fatalf("stderr did not explain permalink failure: %q", stderr.String())
	}
}

func TestParseReplyTargetUsesThreadRoot(t *testing.T) {
	tests := []struct {
		name      string
		permalink string
		wantTs    string
		wantErr   bool
	}{
		{
			name:      "root message",
			permalink: "https://example.slack.com/archives/C12345678/p1700000000000001",
			wantTs:    "1700000000.000001",
		},
		{
			name:      "existing reply",
			permalink: "https://example.slack.com/archives/C12345678/p1700000001000002?thread_ts=1700000000.000001&cid=C12345678",
			wantTs:    "1700000000.000001",
		},
		{
			name:      "invalid thread timestamp",
			permalink: "https://example.slack.com/archives/C12345678/p1700000001000002?thread_ts=invalid",
			wantErr:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := parseReplyTarget(test.permalink)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseReplyTarget() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (target.channelID != "C12345678" || target.threadTs != test.wantTs) {
				t.Fatalf("target = %#v, want channel C12345678 thread %s", target, test.wantTs)
			}
		})
	}
}

func TestReplyPostErrorGuidesRecovery(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMessage string
		wantAction  string
	}{
		{
			name:        "missing write scope",
			err:         &api.MethodError{Code: "missing_scope"},
			wantMessage: "Slack cannot post replies with the current credential.",
			wantAction:  "Add chat:write",
		},
		{
			name:        "not in conversation",
			err:         &api.MethodError{Code: "not_in_channel"},
			wantMessage: "Slack cannot reply because the authenticated user is not in that conversation.",
			wantAction:  "Open or join the conversation",
		},
		{
			name:        "transport delivery uncertain",
			err:         errors.New("connection closed after request"),
			wantMessage: "Slack did not confirm whether the reply was posted.",
			wantAction:  "Inspect the thread before retrying.",
		},
		{
			name:        "Slack internal outcome uncertain",
			err:         &api.MethodError{Code: "internal_error"},
			wantMessage: "Slack did not confirm whether the reply was posted.",
			wantAction:  "Inspect the thread before retrying.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := replyPostError(test.err)
			var commandErr *CommandError
			if !errors.As(err, &commandErr) {
				t.Fatalf("replyPostError() = %T, want CommandError", err)
			}
			if commandErr.Message != test.wantMessage {
				t.Fatalf("message = %q, want %q", commandErr.Message, test.wantMessage)
			}
			if !strings.Contains(commandErr.Action, test.wantAction) {
				t.Fatalf("action = %q, want phrase %q", commandErr.Action, test.wantAction)
			}
		})
	}
}
