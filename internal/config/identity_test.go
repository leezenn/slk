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

func TestIdentityRequiresCanonicalIDsAndDerivesOpaqueStableKeys(t *testing.T) {
	for _, test := range []struct {
		teamID string
		userID string
	}{
		{"", "U111"},
		{"T111", ""},
		{" T111", "U111"},
		{"T111", "U111 "},
	} {
		if _, err := NewIdentity(test.teamID, test.userID); err == nil {
			t.Fatalf("NewIdentity(%q, %q) succeeded", test.teamID, test.userID)
		}
	}

	first, err := NewIdentity("T111", "U111")
	if err != nil {
		t.Fatal(err)
	}
	same, _ := NewIdentity("T111", "U111")
	other, _ := NewIdentity("T111", "U222")
	firstKey, _ := first.Namespace()
	sameKey, _ := same.Namespace()
	otherKey, _ := other.Namespace()
	if firstKey != sameKey || firstKey == otherKey || !validNamespace(firstKey) {
		t.Fatalf("identity keys = %q %q %q", firstKey, sameKey, otherKey)
	}
	if strings.Contains(firstKey, first.TeamID) || strings.Contains(firstKey, first.UserID) {
		t.Fatalf("identity key exposed canonical IDs: %q", firstKey)
	}
}

func TestBindIdentityMigratesReleasedPreferencesInOneFile(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "config.json")
	legacy := `{
		"disabled": true,
		"deny_mutations": ["delete"],
		"message_prefix": "Migrated prefix",
		"message_presentation": "always-expanded",
		"formatting": ["em-dash-to-spaced-hyphen"]
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	first, _ := NewIdentity("T111", "U111")
	other, _ := NewIdentity("T111", "U222")

	document, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preferences, changed, err := document.BindIdentity(first)
	if err != nil || !changed {
		t.Fatalf("BindIdentity() = %#v, %v, %v", preferences, changed, err)
	}
	settings := Merge(document, preferences)
	if !settings.Disabled || !settings.MutationDenied(MutationDelete) ||
		settings.MessagePrefix != "Migrated prefix" || settings.MessagePresentation != presentation.AlwaysExpanded ||
		!settings.FormattingEnabled(textformat.ModuleEmDashToSpacedHyphen) {
		t.Fatalf("migrated settings = %#v", settings)
	}
	if err := SaveFile(path, document); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatal(err)
	}
	for _, legacyKey := range []string{"message_prefix", "message_presentation", "formatting"} {
		if _, present := stored[legacyKey]; present {
			t.Fatalf("legacy key %q remained after migration: %s", legacyKey, contents)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "identities")); !os.IsNotExist(err) {
		t.Fatalf("migration created a second config tree: %v", err)
	}

	reloaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, changed, err := reloaded.BindIdentity(first)
	if err != nil || changed || restored.Effective().MessagePrefix != "Migrated prefix" {
		t.Fatalf("same identity restore = %#v, %v, %v", restored, changed, err)
	}
	otherPreferences, changed, err := reloaded.BindIdentity(other)
	if err != nil || changed || otherPreferences.Effective().MessagePrefix != DefaultMessagePrefix {
		t.Fatalf("other identity inherited preferences: %#v, %v, %v", otherPreferences, changed, err)
	}
}

func TestMachineUpdatePreservesUnassignedReleasedPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"message_prefix":"preserved"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	document.Disabled = true
	document.DeniedMutations = []Mutation{MutationWrite}
	if err := SaveFile(path, document); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := NewIdentity("T111", "U111")
	preferences, changed, err := reloaded.BindIdentity(identity)
	if err != nil || !changed || preferences.Effective().MessagePrefix != "preserved" {
		t.Fatalf("preserved migration = %#v, %v, %v", preferences, changed, err)
	}
}

func TestIdentityPreferenceUpdatesStayIndependentInsideAggregate(t *testing.T) {
	first, _ := NewIdentity("T111", "U111")
	second, _ := NewIdentity("T111", "U222")
	firstPrefix := "first"
	secondPrefix := "second"
	document := Document{}
	if err := document.SetPreferences(first, Preferences{MessagePrefix: &firstPrefix}); err != nil {
		t.Fatal(err)
	}
	if err := document.SetPreferences(second, Preferences{MessagePrefix: &secondPrefix}); err != nil {
		t.Fatal(err)
	}
	firstPreferences, _ := document.Preferences(first)
	secondPreferences, _ := document.Preferences(second)
	if firstPreferences.Effective().MessagePrefix != firstPrefix || secondPreferences.Effective().MessagePrefix != secondPrefix {
		t.Fatalf("identity preferences = %#v %#v", firstPreferences, secondPreferences)
	}

	if err := document.SetPreferences(first, Preferences{}); err != nil {
		t.Fatal(err)
	}
	firstPreferences, _ = document.Preferences(first)
	secondPreferences, _ = document.Preferences(second)
	if firstPreferences.Effective().MessagePrefix != DefaultMessagePrefix || secondPreferences.Effective().MessagePrefix != secondPrefix {
		t.Fatalf("reset affected another identity: %#v %#v", firstPreferences, secondPreferences)
	}
}
