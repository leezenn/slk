package cmd

import (
	"strings"
	"testing"
)

func TestFormatWhoami(t *testing.T) {
	identity := whoamiIdentity{
		Handle:      "owner",
		DisplayName: "Workspace Owner",
		UserID:      "U12345678",
		Workspace:   "Example Workspace",
		WorkspaceID: "T12345678",
	}

	got := formatWhoami(identity)
	for _, want := range []string{
		"Handle:       @owner",
		"Display name: Workspace Owner",
		"User ID:      U12345678",
		"Workspace:    Example Workspace (T12345678)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("whoami output missing %q:\n%s", want, got)
		}
	}
}
