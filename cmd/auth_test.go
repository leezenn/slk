package cmd

import (
	"context"
	"reflect"
	"strings"
	"testing"

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

func TestAuthAccessSummary(t *testing.T) {
	allReadScopes := []string{
		"channels:history", "channels:read",
		"groups:history", "groups:read",
		"im:history", "im:read",
		"mpim:history", "mpim:read",
		"reactions:read", "search:read", "users:read", "files:read",
	}

	tests := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{
			name:   "all current read features",
			scopes: allReadScopes,
			want:   []string{"Access: verified for all current slk read features."},
		},
		{
			name: "limited access explains features and recovery",
			scopes: []string{
				"channels:history", "channels:read",
				"groups:history", "groups:read",
				"im:history", "im:read",
				"mpim:history", "mpim:read",
				"reactions:read", "users:read",
			},
			want: []string{
				"Access is limited: workspace search and file downloads may not work.",
				"Missing Slack scopes: search:read, files:read.",
				"Update the Slack app permissions, reinstall it, then run 'slk auth --interactive'.",
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
