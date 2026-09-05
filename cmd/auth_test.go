package cmd

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
)

func TestInteractiveAuthPromptsWhenCredentialExists(t *testing.T) {
	store := &fakeCredentialStore{getResult: auth.Result{Token: "configured", Source: auth.SourceKeychain}}
	code, stdout, stderr := runIsolated(t, isolatedDependencies(store), context.Background(), "auth", "--interactive")
	if code != 130 || stderr != "Operation interrupted.\n" {
		t.Fatalf("interactive auth = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Let's reconnect Slack with a new token.") {
		t.Fatalf("interactive auth did not explain reconnect: %q", stdout)
	}
	if store.getCalls != 0 {
		t.Fatalf("interactive auth inspected existing credential %d times", store.getCalls)
	}
}

func TestAuthRejectsIncompleteCanonicalIdentityBeforeCredentialOrNamespaceMutation(t *testing.T) {
	for _, result := range []*api.AuthTestResult{
		{TeamID: "", UserID: "U111"},
		{TeamID: "T111", UserID: ""},
	} {
		credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-old", Source: auth.SourceKeychain}}
		configuration := &fakeConfigStore{}
		deps := isolatedDependencies(credentials)
		deps.Configuration = configuration
		deps.ValidateToken = func(context.Context, string, io.Writer) (*api.AuthTestResult, error) { return result, nil }

		code, stdout, stderr := runIsolated(t, deps, context.Background(), "auth", "xoxp-new")
		if code != 1 || stdout != "" || !strings.Contains(stderr, "complete canonical identity") {
			t.Fatalf("incomplete auth = code %d stdout %q stderr %q", code, stdout, stderr)
		}
		if credentials.setCalls != 0 || credentials.getResult.Token != "xoxp-old" || configuration.saveCalls != 0 {
			t.Fatalf("incomplete auth mutated state: credentials %#v config %#v", credentials, configuration)
		}
	}
}

func TestReauthenticationFailurePreservesStoredCredential(t *testing.T) {
	tests := []struct {
		name       string
		validate   func(context.Context, string, io.Writer) (*api.AuthTestResult, error)
		loadErr    error
		wantPhrase string
	}{
		{
			name: "auth validation failure",
			validate: func(context.Context, string, io.Writer) (*api.AuthTestResult, error) {
				return nil, errors.New("synthetic auth failure")
			},
			wantPhrase: "Slack rejected the credential",
		},
		{
			name: "config load failure",
			validate: func(context.Context, string, io.Writer) (*api.AuthTestResult, error) {
				return &api.AuthTestResult{TeamID: "T222", UserID: "U222"}, nil
			},
			loadErr:    errors.New("synthetic config failure"),
			wantPhrase: "could not load its configuration",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-old", Source: auth.SourceKeychain}}
			configuration := &fakeConfigStore{loadErr: test.loadErr}
			deps := isolatedDependencies(credentials)
			deps.Configuration = configuration
			deps.ValidateToken = test.validate

			code, stdout, stderr := runIsolated(t, deps, context.Background(), "auth", "xoxp-new")
			if code != 1 || stdout != "" || !strings.Contains(stderr, test.wantPhrase) {
				t.Fatalf("reauth failure = code %d stdout %q stderr %q", code, stdout, stderr)
			}
			if credentials.setCalls != 0 || credentials.getResult.Token != "xoxp-old" || configuration.saveCalls != 0 {
				t.Fatalf("reauth failure replaced prior state: credentials %#v config %#v", credentials, configuration)
			}
		})
	}
}

func TestAuthAccessSummary(t *testing.T) {
	allReadScopes := []string{
		"channels:history", "channels:read",
		"groups:history", "groups:read",
		"im:history", "im:read",
		"mpim:history", "mpim:read",
		"search:read", "users:read", "files:read",
	}

	tests := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{
			name:   "all current read features without writing",
			scopes: allReadScopes,
			want: []string{
				"Access: verified for all current slk read features.",
				"Message mutations: write, reply, edit, replace, and delete require chat:write.",
			},
		},
		{
			name:   "message writing available",
			scopes: append(append([]string{}, allReadScopes...), "chat:write"),
			want: []string{
				"Access: verified for all current slk read features.",
				"Message mutations: write, reply, edit, replace, and delete are available.",
			},
		},
		{
			name: "limited access explains features and recovery",
			scopes: []string{
				"channels:history", "channels:read",
				"groups:history", "groups:read",
				"im:history", "im:read",
				"mpim:history", "mpim:read",
				"users:read",
			},
			want: []string{
				"Access is limited: workspace discovery and file downloads may not work.",
				"Missing Slack scopes: search:read, files:read.",
				"Update the Slack app permissions, reinstall it, then run 'slk auth --interactive'.",
				"Message mutations: write, reply, edit, replace, and delete require chat:write.",
			},
		},
		{
			name: "Slack omitted permissions",
			want: []string{"Access: Slack accepted the token but did not report enough information to verify feature permissions."},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := authAccessSummary(test.scopes); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("authAccessSummary() = %#v, want %#v", got, test.want)
			}
		})
	}
}
