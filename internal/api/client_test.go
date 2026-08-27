package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAuthTestUsesConfiguredContextAndReturnsScopes(t *testing.T) {
	type contextKey string
	const (
		key  contextKey = "request-marker"
		want            = "configured-command-context"
	)

	client := NewClient("test-token")
	client.SetContext(context.WithValue(context.Background(), key, want))
	called := false
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		if got := req.Context().Value(key); got != want {
			t.Fatalf("request context marker = %v, want %q", got, want)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"user_id":"U12345678"}`)),
			Header: http.Header{
				"X-Oauth-Scopes": {"users:read, channels:read, users:read"},
			},
			Request: req,
		}, nil
	})

	result, err := client.AuthTest()
	if err != nil {
		t.Fatalf("AuthTest returned error: %v", err)
	}
	if got := strings.Join(result.Scopes, ","); got != "channels:read,users:read" {
		t.Fatalf("scopes = %q, want normalized scopes", got)
	}
	if !called {
		t.Fatal("configured transport was not called")
	}
}

func TestPostMessageReplyAndGetPermalink(t *testing.T) {
	const (
		channelID = "C12345678"
		threadTs  = "1700000000.000001"
		replyTs   = "1700000001.000002"
		text      = "We found the issue and will ship the fix tomorrow."
		prefix    = ":mechanical_arm: agent assisted response."
		permalink = "https://example.slack.com/archives/C12345678/p1700000001000002?thread_ts=1700000000.000001&cid=C12345678"
	)

	client := NewClient("test-token")
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.ParseForm(); err != nil {
			t.Fatalf("parsing request form: %v", err)
		}
		var body string
		switch req.URL.Path {
		case "/api/chat.postMessage":
			if got := req.Form.Get("channel"); got != channelID {
				t.Fatalf("post channel = %q, want %q", got, channelID)
			}
			if got := req.Form.Get("thread_ts"); got != threadTs {
				t.Fatalf("post thread_ts = %q, want %q", got, threadTs)
			}
			if got, want := req.Form.Get("text"), prefix+"\n\n"+text; got != want {
				t.Fatalf("post fallback text = %q, want %q", got, want)
			}
			var blocks []slackBlock
			if err := json.Unmarshal([]byte(req.Form.Get("blocks")), &blocks); err != nil {
				t.Fatalf("decoding post blocks: %v", err)
			}
			if len(blocks) != 2 || blocks[0].Type != "context" || len(blocks[0].Elements) != 1 || blocks[0].Elements[0].Type != "mrkdwn" || blocks[0].Elements[0].Text != prefix {
				t.Fatalf("prefix context block = %#v", blocks)
			}
			if blocks[1].Type != "section" || blocks[1].Text == nil || blocks[1].Text.Text != text {
				t.Fatalf("message section block = %#v", blocks[1])
			}
			body = `{"ok":true,"channel":"` + channelID + `","ts":"` + replyTs + `"}`
		case "/api/chat.getPermalink":
			if got := req.Form.Get("channel"); got != channelID {
				t.Fatalf("permalink channel = %q, want %q", got, channelID)
			}
			if got := req.Form.Get("message_ts"); got != replyTs {
				t.Fatalf("permalink message_ts = %q, want %q", got, replyTs)
			}
			body = `{"ok":true,"permalink":"` + permalink + `"}`
		default:
			t.Fatalf("unexpected Slack method path %q", req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	posted, err := client.PostMessage(PostMessageRequest{
		ChannelID: channelID,
		ThreadTs:  threadTs,
		Text:      text,
		Prefix:    prefix,
	})
	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	if posted.Channel != channelID || posted.Ts != replyTs {
		t.Fatalf("posted result = %#v", posted)
	}
	gotPermalink, err := client.GetPermalink(posted.Channel, posted.Ts)
	if err != nil {
		t.Fatalf("GetPermalink returned error: %v", err)
	}
	if gotPermalink != permalink {
		t.Fatalf("permalink = %q, want %q", gotPermalink, permalink)
	}
}

func TestPostMessageTopLevelOmitsThreadAndBlocksWhenPrefixIsEmpty(t *testing.T) {
	client := NewClient("test-token")
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := req.Form.Get("text"); got != "hello" {
			t.Fatalf("post text = %q, want hello", got)
		}
		if _, present := req.Form["thread_ts"]; present {
			t.Fatalf("top-level post unexpectedly included thread_ts: %q", req.Form.Get("thread_ts"))
		}
		if _, present := req.Form["blocks"]; present {
			t.Fatalf("post unexpectedly included blocks: %q", req.Form.Get("blocks"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"channel":"C12345678","ts":"1700000001.000002"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	if _, err := client.PostMessage(PostMessageRequest{ChannelID: "C12345678", Text: "hello"}); err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
}

func TestReplyBlocksSplitLongTextWithoutDataLoss(t *testing.T) {
	text := strings.Repeat("å", slackSectionTextLimit+1)
	blocks := messageBlocks(text, "prefix")
	if len(blocks) != 3 {
		t.Fatalf("block count = %d, want one context and two sections", len(blocks))
	}
	if blocks[0].Type != "context" {
		t.Fatalf("first block type = %q, want context", blocks[0].Type)
	}
	if got := blocks[1].Text.Text + blocks[2].Text.Text; got != text {
		t.Fatal("split section text did not reconstruct the original Unicode message")
	}
	if len([]rune(blocks[1].Text.Text)) > slackSectionTextLimit || len([]rune(blocks[2].Text.Text)) > slackSectionTextLimit {
		t.Fatal("split section exceeded Slack's text limit")
	}
}

func TestPostMessageReturnsTypedSlackRejection(t *testing.T) {
	client := NewClient("test-token")
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":false,"error":"missing_scope"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	_, err := client.PostMessage(PostMessageRequest{ChannelID: "C12345678", Text: "hello", Prefix: "prefix"})
	var methodErr *MethodError
	if !errors.As(err, &methodErr) || methodErr.Code != "missing_scope" {
		t.Fatalf("PostMessage error = %v, want typed missing_scope", err)
	}
}

func TestValidateSlackFileURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "files host", url: "https://files.slack.com/files-pri/T-F/report.pdf"},
		{name: "root host", url: "https://slack.com/files-pri/T-F/report.pdf"},
		{name: "HTTPS port", url: "https://files.slack.com:443/files-pri/T-F/report.pdf"},
		{name: "non-HTTPS", url: "http://files.slack.com/files-pri/T-F/report.pdf", wantErr: true},
		{name: "untrusted host", url: "https://attacker.example/report.pdf", wantErr: true},
		{name: "suffix spoof", url: "https://files.slack.com.attacker.example/report.pdf", wantErr: true},
		{name: "userinfo", url: "https://user@files.slack.com/report.pdf", wantErr: true},
		{name: "unexpected port", url: "https://files.slack.com:8443/report.pdf", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSlackFileURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateSlackFileURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSlackFileURLRedactsMalformedInput(t *testing.T) {
	const malformed = "https://files.slack.com/private/%zz/secret-report.pdf"

	err := validateSlackFileURL(malformed)
	if err == nil || err.Error() != "invalid Slack file URL" {
		t.Fatalf("error = %v, want generic invalid-URL error", err)
	}
	if strings.Contains(err.Error(), "secret-report") || strings.Contains(err.Error(), "files.slack.com") {
		t.Fatalf("error leaked malformed private URL: %v", err)
	}
}

func TestDownloadFileRejectsUntrustedHostBeforeRequest(t *testing.T) {
	called := false
	client := NewClient("test-token")
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		t.Fatalf("unexpected request to %s", req.URL)
		return nil, nil
	})

	_, _, err := client.DownloadFile("https://attacker.example/report.pdf")
	if err == nil || !strings.Contains(err.Error(), "refusing non-Slack file host") {
		t.Fatalf("error = %v, want untrusted-host rejection", err)
	}
	if called {
		t.Fatal("transport was called for an untrusted host")
	}
}

func TestDownloadFileRedactsPrivateURLFromRequestError(t *testing.T) {
	const privateURL = "https://files.slack.com/files-pri/T-F/secret-report.pdf"
	client := NewClient("test-token")
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("dial timeout")
	})

	_, _, err := client.DownloadFile(privateURL)
	if err == nil {
		t.Fatal("DownloadFile returned nil error")
	}
	if !strings.Contains(err.Error(), "dial timeout") {
		t.Fatalf("error omitted the network cause: %v", err)
	}
	if strings.Contains(err.Error(), privateURL) || strings.Contains(err.Error(), "files.slack.com") {
		t.Fatalf("error leaked the private download URL: %v", err)
	}
}

func TestDownloadFileRejectsUntrustedRedirectWithoutForwardingToken(t *testing.T) {
	var hosts []string
	client := NewClient("test-token")
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Hostname())
		if req.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("trusted Slack request missing bearer token")
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"https://attacker.example/report.pdf"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})

	_, _, err := client.DownloadFile("https://files.slack.com/files-pri/T-F/report.pdf")
	if err == nil || !strings.Contains(err.Error(), "refusing Slack file redirect") {
		t.Fatalf("error = %v, want redirect rejection", err)
	}
	if len(hosts) != 1 || hosts[0] != "files.slack.com" {
		t.Fatalf("requested hosts = %v, want only files.slack.com", hosts)
	}
}

func TestDownloadFileAllowsTrustedRedirectAndForwardsToken(t *testing.T) {
	var hosts []string
	client := NewClient("test-token")
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Hostname())
		if req.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("request to %s missing bearer token", req.URL.Hostname())
		}
		if req.URL.Hostname() == "files.slack.com" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": {"https://slack.com/files-pri/T-F/report.pdf"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader("%PDF-")),
			ContentLength: 5,
			Header:        make(http.Header),
			Request:       req,
		}, nil
	})

	body, size, err := client.DownloadFile("https://files.slack.com/files-pri/T-F/report.pdf")
	if err != nil {
		t.Fatalf("DownloadFile returned error: %v", err)
	}
	defer body.Close()
	if size != 5 {
		t.Fatalf("size = %d, want 5", size)
	}
	if len(hosts) != 2 || hosts[0] != "files.slack.com" || hosts[1] != "slack.com" {
		t.Fatalf("requested hosts = %v, want trusted redirect chain", hosts)
	}
}
