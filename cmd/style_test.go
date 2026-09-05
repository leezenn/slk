package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
	"github.com/leezenn/slk/internal/profile"
)

func styleTestContent(pattern string) profile.Content {
	return profile.Content{
		LanguagePatterns:  []string{pattern, "Uses concise factual statements"},
		Limitations:       []string{},
		SyntheticExamples: []string{"Synthetic example: The change is ready."},
	}
}

func styleTestCoverage() profile.Coverage {
	return profile.Coverage{
		Count:      12,
		Limit:      100,
		WindowFrom: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		WindowTo:   time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
		Completion: profile.CompletionSourceExhausted,
	}
}

func styleDependencies(t *testing.T) (Dependencies, profile.Store, config.Identity) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	identity := syntheticIdentity()
	configuration := config.NewStore()
	profiles := profile.NewStore()
	deps := isolatedDependencies(&fakeCredentialStore{getResult: auth.Result{Token: "xoxp-synthetic", Source: auth.SourceKeychain}})
	deps.Configuration = configuration
	deps.Profiles = profiles
	deps.Now = func() time.Time { return time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC) }
	return deps, profiles, identity
}

func TestStyleIsAlwaysVisibleInOfflineRootHelp(t *testing.T) {
	deps := forbiddenDependencies(t)
	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "\n  style ") ||
		!strings.Contains(stdout, "general scope; no Slack or identity state is read for help") ||
		!strings.Contains(stdout, "linguistic analysis") || !strings.Contains(stdout, "slk style prepare") ||
		!strings.Contains(stdout, "slk style create") {
		t.Fatalf("style root help = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestMachineDisabledRefusesStyleBeforeIdentityOrProfileAccess(t *testing.T) {
	deps := forbiddenDependencies(t)
	deps.Configuration = &fakeConfigStore{document: config.Document{Disabled: true}}
	code, stdout, stderr := runIsolated(t, deps, context.Background(), "style")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "unavailable because slk is disabled") {
		t.Fatalf("machine-disabled style = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestStyleHelpIsDependencyFree(t *testing.T) {
	for _, args := range [][]string{
		{"style", "--help"}, {"style", "prepare", "--help"}, {"style", "create", "--help"},
		{"style", "use", "--help"}, {"style", "review", "--help"},
		{"style", "adjust", "--help"}, {"style", "approve", "--help"},
	} {
		deps := forbiddenDependencies(t)
		code, stdout, stderr := runIsolated(t, deps, context.Background(), args...)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage:") {
			t.Fatalf("%v help = code %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), "style", "prepare", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, stylePreparationGuide) ||
		!strings.Contains(stdout, "6 through 200") || !strings.Contains(stdout, "--limit") ||
		!strings.Contains(stdout, "default 100") || !strings.Contains(stdout, "unmarked_text: string") ||
		!strings.Contains(stdout, "detected_structure: array of strings") || !strings.Contains(stdout, "blockquote_omitted") ||
		!strings.Contains(stdout, "composition provenance is\nunknown") {
		t.Fatalf("prepare help omitted forensics guidance: code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runIsolated(t, forbiddenDependencies(t), context.Background(), "style", "create", "--help")
	for _, field := range []string{
		"coverage",
		"nonzero limit",
		"language_patterns: array of strings (1-64 non-empty bounded entries)",
		"limitations: array of strings (0-64 non-empty bounded entries; [] is valid)",
		"synthetic_examples: array of strings (at most 8 non-empty bounded entries)",
	} {
		if code != 0 || stderr != "" || !strings.Contains(stdout, field) {
			t.Fatalf("create help omitted %q: code %d stdout %q stderr %q", field, code, stdout, stderr)
		}
	}
}

func TestStyleRequiresAuthenticationBeforeProfileAccess(t *testing.T) {
	deps := isolatedDependencies(&fakeCredentialStore{getErr: errors.New("credential not found")})
	code, stdout, stderr := runIsolated(t, deps, context.Background(), "style")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "Slack authentication is not configured") {
		t.Fatalf("unauthenticated style = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestStyleCatalogReportsAbsentGeneralProfile(t *testing.T) {
	deps, _, _ := styleDependencies(t)
	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--json", "style")
	if code != 0 || stderr != "" {
		t.Fatalf("style catalog = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	var payload struct {
		Scope   string `json:"scope"`
		Profile struct {
			State        profile.State     `json:"state"`
			Continuation styleContinuation `json:"continuation"`
		} `json:"profile"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Scope != "general" || payload.Profile.State != profile.StateAbsent || payload.Profile.Continuation.Command != "slk --json style prepare" {
		t.Fatalf("catalog payload = %#v", payload)
	}
}

func TestStyleGeneralLifecycle(t *testing.T) {
	deps, store, identity := styleDependencies(t)
	creation, err := json.Marshal(profile.CreateInput{Coverage: styleTestCoverage(), Profile: styleTestContent("first draft")})
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runIsolatedWithInput(t, deps, string(creation), "style", "create")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "State: draft") || !strings.Contains(stdout, "first draft") {
		t.Fatalf("create = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	draft, err := store.Review(identity)
	if err != nil {
		t.Fatal(err)
	}
	code, duplicateStdout, duplicateStderr := runIsolatedWithInput(t, deps, string(creation), "style", "create")
	if code != 1 || duplicateStdout != "" || !strings.Contains(duplicateStderr, "general style profile already exists") {
		t.Fatalf("duplicate create = code %d stdout %q stderr %q", code, duplicateStdout, duplicateStderr)
	}

	code, stdout, stderr = runIsolated(t, deps, context.Background(), "style", "use")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "draft-only") || !strings.Contains(stderr, "slk style review") {
		t.Fatalf("draft use = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runIsolated(t, deps, context.Background(), "--json", "style", "review")
	if code != 0 || stderr != "" || !strings.Contains(stdout, draft.Digest) || !strings.Contains(stdout, "slk style approve --digest") {
		t.Fatalf("review = code %d stdout %q stderr %q", code, stdout, stderr)
	}

	code, stdout, stderr = runIsolated(t, deps, context.Background(), "style", "approve", "--digest", draft.Digest)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "State: approved") {
		t.Fatalf("approve = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runIsolated(t, deps, context.Background(), "--json", "style", "use")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"scope": "general"`) || !strings.Contains(stdout, "first draft") {
		t.Fatalf("use = code %d stdout %q stderr %q", code, stdout, stderr)
	}

	adjusted := styleTestContent("replacement draft")
	contents, _ := json.Marshal(adjusted)
	code, stdout, stderr = runIsolatedWithInput(t, deps, string(contents), "style", "adjust")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "replacement draft") || !strings.Contains(stdout, "Approve exactly:") {
		t.Fatalf("adjust = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runIsolated(t, deps, context.Background(), "style", "approve", "--digest", draft.Digest)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "missing, replaced, or stale") {
		t.Fatalf("stale approve = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	used, err := store.Use(identity)
	if err != nil || used.Digest != draft.Digest {
		t.Fatalf("stale approval changed approved profile: %#v, %v", used, err)
	}
}

func TestStyleCreateRejectsUnexpectedEnvelopeBeforeDependencies(t *testing.T) {
	valid, err := json.Marshal(profile.CreateInput{Coverage: styleTestCoverage(), Profile: styleTestContent("strict create")})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.TrimSuffix(string(valid), "}") + `,"evidence":[{"text":"private"}]}`
	code, stdout, stderr := runIsolatedWithInput(t, forbiddenDependencies(t), input, "style", "create")
	if code != 1 || stdout != "" || !strings.Contains(stderr, `unknown field "evidence"`) {
		t.Fatalf("strict create = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestStyleAdjustRejectsUnknownFieldsBeforeDependencies(t *testing.T) {
	deps := forbiddenDependencies(t)
	input := `{"language_patterns":["x"],"limitations":[],"raw_messages":["x"]}`
	code, stdout, stderr := runIsolatedWithInput(t, deps, input, "style", "adjust")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "unknown field") {
		t.Fatalf("invalid adjustment = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestStyleSchemaHelpIsSharedAcrossPrepareCreateAndAdjust(t *testing.T) {
	for _, args := range [][]string{{"style", "prepare", "--help"}, {"style", "create", "--help"}, {"style", "adjust", "--help"}} {
		code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%v help = code %d stderr %q", args, code, stderr)
		}
		if !strings.Contains(stdout, styleSemanticFieldsHelp) {
			t.Fatalf("%v omitted the shared semantic schema help", args)
		}
	}
}

func TestIncompatibleStyleGuidancePreservesFileAndOffersNoUnsupportedAction(t *testing.T) {
	deps, _, identity := styleDependencies(t)
	namespace, err := identity.Namespace()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "slk", "profiles", namespace, "general.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"schema_version":1,"draft":{"content":{"summary":"old profile"}}}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(styleTestContent("Short clauses"))
	if err != nil {
		t.Fatal(err)
	}
	creation, err := json.Marshal(profile.CreateInput{Coverage: styleTestCoverage(), Profile: styleTestContent("Short clauses")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		input string
		code  int
	}{
		{"status", []string{"style"}, "", 0},
		{"status-json", []string{"--json", "style"}, "", 0},
		{"use", []string{"style", "use"}, "", 1},
		{"review", []string{"style", "review"}, "", 1},
		{"adjust", []string{"style", "adjust"}, string(content), 1},
		{"approve", []string{"style", "approve", "--digest", strings.Repeat("a", 64)}, "", 1},
		{"create", []string{"style", "create"}, string(creation), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runIsolatedWithInput(t, deps, tc.input, tc.args...)
			if code != tc.code {
				t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			output := stdout + stderr
			for _, unsupported := range []string{"regenerate", "slk style review", "slk style adjust", "slk --json style prepare"} {
				if strings.Contains(strings.ToLower(output), unsupported) {
					t.Fatalf("unsupported continuation %q in %q", unsupported, output)
				}
			}
			if tc.name == "create" {
				if !strings.Contains(output, "Inspect its state with 'slk style'") {
					t.Fatalf("missing state inspection guidance: %q", output)
				}
			} else if !strings.Contains(output, "Automatic replacement is unavailable") || !strings.Contains(output, "ask the human") {
				t.Fatalf("missing incompatibility decision boundary: %q", output)
			}
			if tc.name == "status-json" {
				var payload struct {
					Profile styleProfileStatus `json:"profile"`
				}
				if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Profile.State != profile.StateIncompatible || payload.Profile.Continuation.Command != "" {
					t.Fatalf("unexpected continuation: %#v", payload.Profile)
				}
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, legacy) {
				t.Fatalf("old profile changed: %q, %v", got, err)
			}
		})
	}
}

func TestStylePlainRendererUsesSparseLinguisticHeadings(t *testing.T) {
	deps, _, _ := styleDependencies(t)
	creation, err := json.Marshal(profile.CreateInput{Coverage: styleTestCoverage(), Profile: styleTestContent("Uses clipped clauses")})
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runIsolatedWithInput(t, deps, string(creation), "style", "create")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Language patterns:") || !strings.Contains(stdout, "Limitations:") ||
		strings.Contains(stdout, "Summary:") || strings.Contains(stdout, "Tendencies:") || strings.Contains(stdout, "Things to avoid:") || strings.Contains(stdout, "Confidence:") {
		t.Fatalf("sparse renderer = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}
