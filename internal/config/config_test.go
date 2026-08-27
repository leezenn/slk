package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileAppliesSettingsSemantics(t *testing.T) {
	tests := []struct {
		name       string
		content    *string
		wantPrefix string
		wantDenied []Mutation
		wantErr    string
	}{
		{name: "missing file", wantPrefix: DefaultReplyPrefix},
		{name: "missing keys", content: stringPointer(`{}`), wantPrefix: DefaultReplyPrefix},
		{name: "prefix override", content: stringPointer(`{"reply_prefix":"Reviewed by the operator."}`), wantPrefix: "Reviewed by the operator."},
		{name: "explicit empty prefix disables", content: stringPointer(`{"reply_prefix":""}`), wantPrefix: ""},
		{name: "omitted deny list allows all", content: stringPointer(`{}`), wantPrefix: DefaultReplyPrefix},
		{name: "empty deny list allows all", content: stringPointer(`{"deny_mutations":[]}`), wantPrefix: DefaultReplyPrefix},
		{name: "explicit mutations denied", content: stringPointer(`{"deny_mutations":["reply","write"]}`), wantPrefix: DefaultReplyPrefix, wantDenied: []Mutation{MutationReply, MutationWrite}},
		{name: "duplicate mutation deduplicated", content: stringPointer(`{"deny_mutations":["write","write"]}`), wantPrefix: DefaultReplyPrefix, wantDenied: []Mutation{MutationWrite}},
		{name: "unknown mutation rejected", content: stringPointer(`{"deny_mutations":["delete"]}`), wantErr: "unknown command"},
		{name: "wrong deny list type rejected", content: stringPointer(`{"deny_mutations":"write"}`), wantErr: "cannot unmarshal"},
		{name: "whitespace only prefix rejected", content: stringPointer(`{"reply_prefix":"   "}`), wantErr: "must be empty or contain visible text"},
		{name: "wrong prefix type rejected", content: stringPointer(`{"reply_prefix":false}`), wantErr: "cannot unmarshal"},
		{name: "over-specific key rejected", content: stringPointer(`{"agent_assisted_prefix":"hello"}`), wantErr: "unknown field"},
		{name: "multiple values rejected", content: stringPointer(`{} {}`), wantErr: "multiple JSON values"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if test.content != nil {
				if err := os.WriteFile(path, []byte(*test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			settings, err := LoadFile(path)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("LoadFile() error = %v, want phrase %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFile() error = %v", err)
			}
			if settings.ReplyPrefix != test.wantPrefix {
				t.Fatalf("prefix = %q, want %q", settings.ReplyPrefix, test.wantPrefix)
			}
			if len(settings.DeniedMutations) != len(test.wantDenied) {
				t.Fatalf("denied mutations = %v, want %v", settings.DeniedMutations, test.wantDenied)
			}
			for _, mutation := range []Mutation{MutationReply, MutationWrite} {
				wantDenied := containsMutation(test.wantDenied, mutation)
				if got := settings.MutationDenied(mutation); got != wantDenied {
					t.Fatalf("MutationDenied(%q) = %v, want %v", mutation, got, wantDenied)
				}
			}
		})
	}
}

func TestPathUsesXDGConfigHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "slk", "config.json")
	if path != want {
		t.Fatalf("Path() = %q, want %q", path, want)
	}
}

func TestPathRejectsRelativeXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := Path(); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("Path() error = %v, want absolute-path error", err)
	}
}

func stringPointer(value string) *string { return &value }

func containsMutation(mutations []Mutation, target Mutation) bool {
	for _, mutation := range mutations {
		if mutation == target {
			return true
		}
	}
	return false
}
