package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/presentation"
	"github.com/leezenn/slk/internal/textformat"
)

func TestLoadFileReadsMachinePolicyAndReleasedFlatPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{
		"disabled": true,
		"deny_mutations": ["write", "write", "delete"],
		"message_prefix": "Reviewed locally.",
		"message_presentation": "always-expanded",
		"formatting": ["em-dash-to-spaced-hyphen"]
	}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	document, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	machine := document.Effective()
	if !machine.Disabled || !machine.MutationDenied(MutationWrite) || !machine.MutationDenied(MutationDelete) {
		t.Fatalf("machine settings = %#v", machine)
	}
	legacy := document.legacyPreferences()
	if !hasPreferences(legacy) {
		t.Fatal("released flat preferences were not retained for identity binding")
	}
	preferences := legacy.Effective()
	if preferences.MessagePrefix != "Reviewed locally." || preferences.MessagePresentation != presentation.AlwaysExpanded ||
		!preferences.FormattingEnabled(textformat.ModuleEmDashToSpacedHyphen) {
		t.Fatalf("legacy preferences = %#v", preferences)
	}
}

func TestLoadFileDefaultsAndValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "empty", content: `{}`},
		{name: "explicit empty prefix", content: `{"message_prefix":""}`},
		{name: "unknown field", content: `{"reply_prefix":"x"}`, wantErr: "unknown field"},
		{name: "retired style gate", content: `{"style_enabled":true}`, wantErr: "unknown field"},
		{name: "unknown mutation", content: `{"deny_mutations":["launch"]}`, wantErr: "unknown command"},
		{name: "unknown formatting", content: `{"formatting":["sparkles"]}`, wantErr: "unknown module"},
		{name: "invalid presentation", content: `{"message_presentation":"forced"}`, wantErr: "message_presentation"},
		{name: "blank prefix", content: `{"message_prefix":"   "}`, wantErr: "visible text"},
		{name: "multiple values", content: `{} {}`, wantErr: "multiple JSON values"},
		{name: "invalid identity key", content: `{"identities":{"T1-U1":{}}}`, wantErr: "opaque identity key"},
		{name: "unknown identity field", content: `{"identities":{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa":{"prompt":"x"}}}`, wantErr: "unknown field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			document, err := LoadFile(path)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("LoadFile() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if document.Effective().MessagePrefix != DefaultMessagePrefix || document.Effective().MessagePresentation != presentation.Default() {
				t.Fatalf("defaults = %#v", document.Effective())
			}
		})
	}

	missing, err := LoadFile(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || missing.Effective().MessagePrefix != DefaultMessagePrefix {
		t.Fatalf("missing config = %#v, %v", missing, err)
	}
}

func TestSaveFileWritesOneProtectedDeterministicAggregate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	identity, _ := NewIdentity("T111", "U111")
	namespace, _ := identity.Namespace()
	prefix := "Operator reviewed."
	mode := presentation.AlwaysExpanded
	document := Document{
		Disabled:        true,
		DeniedMutations: []Mutation{MutationWrite, MutationDelete, MutationWrite},
	}
	if err := document.SetPreferences(identity, Preferences{
		MessagePrefix:       &prefix,
		MessagePresentation: &mode,
		Formatting:          []textformat.Module{textformat.ModuleEmDashToSpacedHyphen, textformat.ModuleEmDashToSpacedHyphen},
	}); err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(path, document); err != nil {
		t.Fatal(err)
	}

	for _, target := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Dir(path), 0o700}, {path, 0o600}} {
		info, err := os.Stat(target.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != target.mode {
			t.Fatalf("%s mode = %o, want %o", target.path, info.Mode().Perm(), target.mode)
		}
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatal(err)
	}
	if _, present := stored["message_prefix"]; present {
		t.Fatalf("identity preference remained at top level: %s", contents)
	}
	identities, ok := stored["identities"].(map[string]any)
	if !ok || len(identities) != 1 || identities[namespace] == nil {
		t.Fatalf("stored identities = %#v", stored["identities"])
	}
	if strings.Contains(string(contents), identity.TeamID) || strings.Contains(string(contents), identity.UserID) {
		t.Fatalf("stored config exposed canonical identity: %s", contents)
	}

	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preferences, err := loaded.Preferences(identity)
	if err != nil {
		t.Fatal(err)
	}
	settings := Merge(loaded, preferences)
	if !settings.Disabled || settings.MessagePrefix != prefix ||
		settings.MessagePresentation != mode || !settings.MutationDenied(MutationDelete) ||
		!settings.FormattingEnabled(textformat.ModuleEmDashToSpacedHyphen) {
		t.Fatalf("loaded aggregate = %#v", settings)
	}
}

func TestPathUsesAbsoluteXDGConfigHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	path, err := Path()
	if err != nil || path != filepath.Join(base, "slk", "config.json") {
		t.Fatalf("Path() = %q, %v", path, err)
	}

	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := Path(); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative XDG_CONFIG_HOME error = %v", err)
	}
}
