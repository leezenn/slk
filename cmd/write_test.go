package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
	"github.com/spf13/cobra"
)

type fakeWriteClient struct {
	posted       *api.PostMessageResult
	postErr      error
	permalink    string
	permalinkErr error
	request      api.PostMessageRequest
	channel      *api.Channel
	dm           *api.Channel
	resolvedBy   string
}

func (f *fakeWriteClient) PostMessage(request api.PostMessageRequest) (*api.PostMessageResult, error) {
	f.request = request
	return f.posted, f.postErr
}

func (f *fakeWriteClient) GetPermalink(string, string) (string, error) {
	return f.permalink, f.permalinkErr
}

func (f *fakeWriteClient) FindChannelByName(string) (*api.Channel, error) {
	f.resolvedBy = "channel"
	if f.channel == nil {
		return nil, errors.New("channel not found")
	}
	return f.channel, nil
}

func (f *fakeWriteClient) FindDMByUser(string) (*api.Channel, error) {
	f.resolvedBy = "handle"
	if f.dm == nil {
		return nil, errors.New("DM not found")
	}
	return f.dm, nil
}

func (f *fakeWriteClient) FindDMByUserID(string) (*api.Channel, error) {
	f.resolvedBy = "user ID"
	if f.dm == nil {
		return nil, errors.New("DM not found")
	}
	return f.dm, nil
}

func (f *fakeWriteClient) ResolveUser(string) string { return "alex" }

func TestRunWritePostsTopLevelMessageAndReturnsReceipt(t *testing.T) {
	const permalink = "https://example.slack.com/archives/C12345678/p1700000001000002"
	client := &fakeWriteClient{
		posted:    &api.PostMessageResult{Channel: "C12345678", Ts: "1700000001.000002"},
		permalink: permalink,
		channel:   &api.Channel{ID: "C12345678", Name: "general"},
	}
	var stdout, stderr bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)
	command.SetErr(&stderr)

	err := runWrite(command, &rootOptions{}, client, "general", "Deployment complete.", "prefix")
	if err != nil {
		t.Fatalf("runWrite returned error: %v", err)
	}
	wantRequest := api.PostMessageRequest{
		ChannelID: "C12345678",
		Text:      "Deployment complete.",
		Prefix:    "prefix",
	}
	if client.request != wantRequest {
		t.Fatalf("posted request = %#v, want %#v", client.request, wantRequest)
	}
	if client.resolvedBy != "channel" {
		t.Fatalf("target resolved by %q, want channel", client.resolvedBy)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{"Message posted.", permalink, "Open: slk open '" + permalink + "'"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("receipt omitted %q: %q", want, stdout.String())
		}
	}
}

func TestRunWriteResolvesExistingDMAndOmitsThreadFromJSON(t *testing.T) {
	client := &fakeWriteClient{
		posted:    &api.PostMessageResult{Channel: "D12345678", Ts: "1700000001.000002"},
		permalink: "https://example.slack.com/archives/D12345678/p1700000001000002",
		dm:        &api.Channel{ID: "D12345678", User: "U12345678"},
	}
	var stdout bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&stdout)

	if err := runWrite(command, &rootOptions{json: true}, client, "@alex", "hello", "prefix"); err != nil {
		t.Fatalf("runWrite returned error: %v", err)
	}
	if client.resolvedBy != "handle" || client.request.ChannelID != "D12345678" || client.request.ThreadTs != "" {
		t.Fatalf("DM post state = resolver %q request %#v", client.resolvedBy, client.request)
	}
	if strings.Contains(stdout.String(), `"thread_ts"`) {
		t.Fatalf("top-level JSON receipt included thread_ts: %s", stdout.String())
	}
	for _, want := range []string{`"posted": true`, `"channel": "D12345678"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("JSON receipt omitted %q: %s", want, stdout.String())
		}
	}
}

func TestWritePostErrorGuidesUncertainDelivery(t *testing.T) {
	err := writePostError(errors.New("connection closed after request"))
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("writePostError() = %T, want CommandError", err)
	}
	if commandErr.Message != "Slack did not confirm whether the message was posted." {
		t.Fatalf("message = %q", commandErr.Message)
	}
	if commandErr.Action != "Inspect the conversation before retrying." {
		t.Fatalf("action = %q", commandErr.Action)
	}
}
