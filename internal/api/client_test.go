package api

import (
	"context"
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

func TestClientRequestsUseConfiguredContext(t *testing.T) {
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
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})

	if _, err := client.AuthTest(); err != nil {
		t.Fatalf("AuthTest returned error: %v", err)
	}
	if !called {
		t.Fatal("configured transport was not called")
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
