package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
)

func TestUsersToJSONProjectsCanonicalIdentityOnly(t *testing.T) {
	users := []api.User{
		{
			ID: "U12345678", Name: "@alex", RealName: "Top-level name",
			Profile: api.UserProfile{
				DisplayName: "  Alex Display  ", RealName: "Profile name",
				Image48: "https://avatar.example/private-hash.jpg", StatusText: "private status",
			},
			Presence: "active",
		},
		{
			ID: "U87654321", Name: "sam", RealName: "  Sam Resolved  ",
			Deleted: true, IsBot: true,
			Profile: api.UserProfile{Image48: "https://avatar.example/other.jpg"},
		},
	}

	projected := usersToJSON(users)
	if len(projected) != 2 {
		t.Fatalf("projected users = %#v", projected)
	}
	if projected[0] != (userJSON{
		UserID: "U12345678", Handle: "alex", DisplayName: "Alex Display", Presence: "active",
	}) {
		t.Fatalf("first projected user = %#v", projected[0])
	}
	if projected[1] != (userJSON{
		UserID: "U87654321", Handle: "sam", DisplayName: "Sam Resolved", Deleted: true, IsBot: true,
	}) {
		t.Fatalf("second projected user = %#v", projected[1])
	}

	encoded, err := json.Marshal(map[string]interface{}{"ok": true, "users": projected})
	if err != nil {
		t.Fatal(err)
	}
	output := string(encoded)
	for _, forbidden := range []string{`"profile"`, `"image_48"`, `"real_name"`, `"status_text"`, "avatar.example"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("users JSON exposed %q: %s", forbidden, output)
		}
	}
	if strings.Count(output, `"presence"`) != 1 {
		t.Fatalf("users JSON presence projection = %s", output)
	}
}

func TestUsersToJSONFallsBackToHandle(t *testing.T) {
	projected := usersToJSON([]api.User{{ID: "U12345678", Name: "alex"}})
	if len(projected) != 1 || projected[0].DisplayName != "alex" {
		t.Fatalf("handle fallback = %#v", projected)
	}
}
