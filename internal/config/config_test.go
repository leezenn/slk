package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/textformat"
)

func TestLoadFileAppliesSettingsSemantics(t *testing.T) {
	tests := []struct {
		name           string
		content        *string
		wantPrefix     string
		wantOff        bool
		wantDenied     []Mutation
		wantFormatting []textformat.Module
		wantErr        string
	}{
		{name: "missing file", wantPrefix: DefaultMessagePrefix},
		{name: "missing keys", content: stringPointer(`{}`), wantPrefix: DefaultMessagePrefix},
		{name: "prefix override", content: stringPointer(`{"message_prefix":"Reviewed by the operator."}`), wantPrefix: "Reviewed by the operator."},
		{name: "explicit empty prefix disables", content: stringPointer(`{"message_prefix":""}`), wantPrefix: ""},
		{name: "tool disabled", content: stringPointer(`{"disabled":true}`), wantPrefix: DefaultMessagePrefix, wantOff: true},
		{name: "omitted deny list allows all", content: stringPointer(`{}`), wantPrefix: DefaultMessagePrefix},
		{name: "empty deny list allows all", content: stringPointer(`{"deny_mutations":[]}`), wantPrefix: DefaultMessagePrefix},
		{name: "explicit mutations denied", content: stringPointer(`{"deny_mutations":["delete","edit","replace","reply","write"]}`), wantPrefix: DefaultMessagePrefix, wantDenied: []Mutation{MutationDelete, MutationEdit, MutationReplace, MutationReply, MutationWrite}},
		{name: "duplicate mutation deduplicated", content: stringPointer(`{"deny_mutations":["write","write"]}`), wantPrefix: DefaultMessagePrefix, wantDenied: []Mutation{MutationWrite}},
		{name: "formatting explicitly enabled", content: stringPointer(`{"formatting":["em-dash-to-spaced-hyphen"]}`), wantPrefix: DefaultMessagePrefix, wantFormatting: []textformat.Module{textformat.ModuleEmDashToSpacedHyphen}},
		{name: "duplicate formatting deduplicated", content: stringPointer(`{"formatting":["em-dash-to-spaced-hyphen","em-dash-to-spaced-hyphen"]}`), wantPrefix: DefaultMessagePrefix, wantFormatting: []textformat.Module{textformat.ModuleEmDashToSpacedHyphen}},
		{name: "unknown formatting rejected", content: stringPointer(`{"formatting":["unknown"]}`), wantErr: "unknown module"},
		{name: "wrong formatting type rejected", content: stringPointer(`{"formatting":"em-dash-to-spaced-hyphen"}`), wantErr: "cannot unmarshal"},
		{name: "unknown mutation rejected", content: stringPointer(`{"deny_mutations":["unknown"]}`), wantErr: "unknown command"},
		{name: "wrong deny list type rejected", content: stringPointer(`{"deny_mutations":"write"}`), wantErr: "cannot unmarshal"},
		{name: "whitespace only prefix rejected", content: stringPointer(`{"message_prefix":"   "}`), wantErr: "must be empty or contain visible text"},
		{name: "wrong prefix type rejected", content: stringPointer(`{"message_prefix":false}`), wantErr: "cannot unmarshal"},
		{name: "removed reply key rejected", content: stringPointer(`{"reply_prefix":"hello"}`), wantErr: "unknown field"},
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
			if settings.MessagePrefix != test.wantPrefix || settings.Disabled != test.wantOff {
				t.Fatalf("settings = %#v, want prefix %q disabled %v", settings, test.wantPrefix, test.wantOff)
			}
			if len(settings.DeniedMutations) != len(test.wantDenied) {
				t.Fatalf("denied mutations = %v, want %v", settings.DeniedMutations, test.wantDenied)
			}
			for _, mutation := range []Mutation{MutationDelete, MutationEdit, MutationReplace, MutationReply, MutationWrite} {
				wantDenied := containsMutation(test.wantDenied, mutation)
				if got := settings.MutationDenied(mutation); got != wantDenied {
					t.Fatalf("MutationDenied(%q) = %v, want %v", mutation, got, wantDenied)
				}
			}
			wantFormatting := containsFormatting(test.wantFormatting, textformat.ModuleEmDashToSpacedHyphen)
			if got := settings.FormattingEnabled(textformat.ModuleEmDashToSpacedHyphen); got != wantFormatting {
				t.Fatalf("FormattingEnabled() = %v, want %v", got, wantFormatting)
			}
		})
	}
}

func TestLoadDocumentPreservesDefaultAndExplicitEmptyPrefix(t *testing.T) {
	directory := t.TempDir()
	missing, err := LoadDocumentFile(filepath.Join(directory, "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if missing.MessagePrefix != nil {
		t.Fatalf("missing document prefix = %q, want nil", *missing.MessagePrefix)
	}

	path := filepath.Join(directory, "explicit.json")
	if err := os.WriteFile(path, []byte(`{"message_prefix":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	explicit, err := LoadDocumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.MessagePrefix == nil || *explicit.MessagePrefix != "" {
		t.Fatalf("explicit document prefix = %#v, want pointer to empty string", explicit.MessagePrefix)
	}
}

func TestSaveFileWritesProtectedDeterministicConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	prefix := "Operator reviewed."
	document := Document{
		Disabled:        true,
		MessagePrefix:   &prefix,
		DeniedMutations: []Mutation{MutationWrite, MutationDelete, MutationReply, MutationEdit, MutationReplace, MutationWrite},
		Formatting:      []textformat.Module{textformat.ModuleEmDashToSpacedHyphen, textformat.ModuleEmDashToSpacedHyphen},
	}
	if err := SaveFile(path, document); err != nil {
		t.Fatalf("SaveFile() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Disabled        bool                `json:"disabled"`
		MessagePrefix   string              `json:"message_prefix"`
		DeniedMutations []Mutation          `json:"deny_mutations"`
		Formatting      []textformat.Module `json:"formatting"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Disabled || stored.MessagePrefix != prefix {
		t.Fatalf("stored config = %#v", stored)
	}
	wantDenied := []Mutation{MutationDelete, MutationEdit, MutationReplace, MutationReply, MutationWrite}
	if len(stored.DeniedMutations) != len(wantDenied) {
		t.Fatalf("stored denied mutations = %v, want %v", stored.DeniedMutations, wantDenied)
	}
	for index, mutation := range wantDenied {
		if stored.DeniedMutations[index] != mutation {
			t.Fatalf("stored denied mutations = %v, want %v", stored.DeniedMutations, wantDenied)
		}
	}
	wantFormatting := []textformat.Module{textformat.ModuleEmDashToSpacedHyphen}
	if !equalFormatting(stored.Formatting, wantFormatting) {
		t.Fatalf("stored formatting = %v, want %v", stored.Formatting, wantFormatting)
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Disabled || loaded.MessagePrefix != prefix {
		t.Fatalf("loaded settings = %#v", loaded)
	}
	for _, mutation := range wantDenied {
		if !loaded.MutationDenied(mutation) {
			t.Fatalf("loaded settings allow %q: %#v", mutation, loaded)
		}
	}
	if !loaded.FormattingEnabled(textformat.ModuleEmDashToSpacedHyphen) {
		t.Fatalf("loaded settings omitted formatting: %#v", loaded)
	}
}

func TestSaveFileOmitsResetPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveFile(path, Document{}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "message_prefix") {
		t.Fatalf("reset config persisted message_prefix: %s", contents)
	}
	if strings.Contains(string(contents), "formatting") {
		t.Fatalf("default config persisted formatting: %s", contents)
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

func containsFormatting(modules []textformat.Module, target textformat.Module) bool {
	for _, module := range modules {
		if module == target {
			return true
		}
	}
	return false
}

func equalFormatting(left, right []textformat.Module) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
