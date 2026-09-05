package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
	"github.com/leezenn/slk/internal/presentation"
	"github.com/leezenn/slk/internal/textformat"
)

func runIsolatedWithInput(t *testing.T, deps Dependencies, input string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(deps, context.Background(), args, strings.NewReader(input), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestConfigSummaryAndPathAreSecretSafe(t *testing.T) {
	prefix := "Reviewed by operator."
	configuration := &fakeConfigStore{
		path:              "/test/slk/config.json",
		document:          config.Document{DeniedMutations: []config.Mutation{config.MutationWrite}},
		identityDocuments: syntheticIdentityDocuments(config.Preferences{MessagePrefix: &prefix}),
	}
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-secret-value", Source: auth.SourceKeychain}}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "config")
	if code != 0 || stderr != "" {
		t.Fatalf("config = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"Configuration: /test/slk/config.json",
		"Authentication: configured (keychain, xoxp-sec...)",
		`Message prefix (custom): "Reviewed by operator."`,
		"Message presentation (default): slack-managed",
		"Denied mutations: write",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("config summary omitted %q: %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "xoxp-secret-value") {
		t.Fatalf("config summary exposed token: %q", stdout)
	}

	code, stdout, stderr = runIsolated(t, forbiddenDependencies(t), context.Background(), "--json", "config", "path")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"path": "/test/config.json"`) || strings.Contains(stdout, "identity_path") {
		t.Fatalf("offline config path = code %d stdout %q stderr %q", code, stdout, stderr)
	}

	code, stdout, stderr = runIsolated(t, deps, context.Background(), "--json", "config")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"path": "/test/slk/config.json"`) ||
		!strings.Contains(stdout, `"auth_source": "keychain"`) || !strings.Contains(stdout, `"message_presentation": "slack-managed"`) {
		t.Fatalf("config JSON = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, forbidden := range []string{`"token":`, "xoxp-secret-value", "identity_path", "machine_path", "style_enabled"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("config JSON exposed obsolete or secret field %q: %q", forbidden, stdout)
		}
	}
}

func TestConfigIdentityPreferencesRoundTrip(t *testing.T) {
	configuration := &fakeConfigStore{}
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = configuration

	commands := [][]string{
		{"config", "set", "message-prefix", "Custom context"},
		{"config", "set", "message-presentation", "always-expanded"},
		{"config", "formatting", "enable", "em-dash-to-spaced-hyphen"},
	}
	for _, args := range commands {
		code, _, stderr := runIsolated(t, deps, context.Background(), args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%v = code %d stderr %q", args, code, stderr)
		}
	}
	preferences := configuration.syntheticIdentityDocument()
	settings := config.Merge(configuration.document, preferences)
	if preferences.MessagePrefix == nil || *preferences.MessagePrefix != "Custom context" ||
		preferences.MessagePresentation == nil || *preferences.MessagePresentation != presentation.AlwaysExpanded ||
		!settings.FormattingEnabled(textformat.ModuleEmDashToSpacedHyphen) {
		t.Fatalf("stored preferences = %#v", preferences)
	}

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--json", "config")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"message_prefix": "Custom context"`) ||
		!strings.Contains(stdout, `"message_presentation": "always-expanded"`) ||
		!strings.Contains(stdout, "em-dash-to-spaced-hyphen") {
		t.Fatalf("config round-trip = code %d stdout %q stderr %q", code, stdout, stderr)
	}

	for _, args := range [][]string{
		{"config", "reset", "message-prefix"},
		{"config", "reset", "message-presentation"},
		{"config", "formatting", "disable", "em-dash-to-spaced-hyphen"},
	} {
		code, _, stderr := runIsolated(t, deps, context.Background(), args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%v = code %d stderr %q", args, code, stderr)
		}
	}
	preferences = configuration.syntheticIdentityDocument()
	if preferences.MessagePrefix != nil || preferences.MessagePresentation != nil || len(preferences.Formatting) != 0 {
		t.Fatalf("reset preferences = %#v", preferences)
	}
}

func TestConfigRejectsInvalidPreferenceBeforeSaving(t *testing.T) {
	configuration := &fakeConfigStore{}
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = configuration
	for _, args := range [][]string{
		{"config", "set", "message-presentation", "forced"},
		{"config", "formatting", "enable", "unknown"},
	} {
		before := configuration.saveCalls
		code, stdout, stderr := runIsolated(t, deps, context.Background(), args...)
		if code != 1 || stdout != "" || stderr == "" || configuration.saveCalls != before {
			t.Fatalf("invalid %v = code %d stdout %q stderr %q saves %d", args, code, stdout, stderr, configuration.saveCalls)
		}
	}
}

func TestMachinePolicyShapesOfflineHelp(t *testing.T) {
	configuration := &fakeConfigStore{}
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = configuration

	code, _, stderr := runIsolated(t, deps, context.Background(), "config", "deny", "write")
	if code != 0 || stderr != "" {
		t.Fatalf("deny write = code %d stderr %q", code, stderr)
	}
	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--help")
	if code != 0 || stderr != "" || strings.Contains(stdout, "\n  write ") {
		t.Fatalf("denied help = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if code, _, stderr = runIsolated(t, deps, context.Background(), "config", "allow", "write"); code != 0 || stderr != "" {
		t.Fatalf("allow write = code %d stderr %q", code, stderr)
	}

	code, stdout, stderr = runIsolated(t, deps, context.Background(), "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "\n  style ") || !strings.Contains(stdout, "general scope; no Slack or identity state is read for help") {
		t.Fatalf("style help = code %d stdout %q stderr %q", code, stdout, stderr)
	}

	if code, _, stderr = runIsolated(t, deps, context.Background(), "config", "disable"); code != 0 || stderr != "" {
		t.Fatalf("disable = code %d stderr %q", code, stderr)
	}
	code, stdout, stderr = runIsolated(t, deps, context.Background(), "read", "general")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "slk is disabled") {
		t.Fatalf("disabled read = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestValidatedIdentitySelectsOnlyItsPreferences(t *testing.T) {
	first, _ := config.NewIdentity("T111", "U111")
	second, _ := config.NewIdentity("T222", "U222")
	firstKey, _ := first.Namespace()
	secondKey, _ := second.Namespace()
	firstPrefix := "first identity"
	secondPrefix := "second identity"
	configuration := &fakeConfigStore{identityDocuments: map[string]config.Preferences{
		firstKey:  {MessagePrefix: &firstPrefix},
		secondKey: {MessagePrefix: &secondPrefix},
	}}
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-environment", Source: auth.SourceEnv}}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration
	deps.ValidateToken = func(_ context.Context, token string, _ io.Writer) (*api.AuthTestResult, error) {
		return &api.AuthTestResult{TeamID: second.TeamID, UserID: second.UserID}, nil
	}

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "config")
	if code != 0 || stderr != "" || !strings.Contains(stdout, secondPrefix) || strings.Contains(stdout, firstPrefix) || !strings.Contains(stdout, "T222 / U222") {
		t.Fatalf("selected identity = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestDisabledIdentityCommandsStopBeforeAuthentication(t *testing.T) {
	configuration := &fakeConfigStore{document: config.Document{Disabled: true}}
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-configured", Source: auth.SourceKeychain}}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration
	deps.ValidateToken = func(context.Context, string, io.Writer) (*api.AuthTestResult, error) {
		t.Fatal("disabled command validated Slack")
		return nil, nil
	}
	for _, args := range [][]string{
		{"config", "set", "message-prefix", "blocked"},
		{"config", "formatting"},
		{"config", "setup"},
	} {
		code, stdout, stderr := runIsolated(t, deps, context.Background(), args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "identity preferences are unavailable while slk is disabled") {
			t.Fatalf("disabled %v = code %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	if credentials.getCalls != 0 || configuration.saveCalls != 0 {
		t.Fatalf("disabled commands touched auth/config: gets %d saves %d", credentials.getCalls, configuration.saveCalls)
	}
}

func TestConfigSetupChangesMachineAndIdentityWithOneSave(t *testing.T) {
	configuration := &fakeConfigStore{}
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-configured", Source: auth.SourceKeychain}}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration

	input := "y\ny\nCustom context\ny\nn\nn\n\n\n\nn\n"
	code, stdout, stderr := runIsolatedWithInput(t, deps, input, "config", "setup")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Configuration saved to /test/config.json") {
		t.Fatalf("setup = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	preferences := configuration.syntheticIdentityDocument()
	if preferences.MessagePrefix == nil || *preferences.MessagePrefix != "Custom context" ||
		preferences.MessagePresentation == nil || *preferences.MessagePresentation != presentation.AlwaysExpanded {
		t.Fatalf("setup preferences = %#v", preferences)
	}
	settings := configuration.document.Effective()
	if !settings.MutationDenied(config.MutationReply) || !settings.MutationDenied(config.MutationDelete) || settings.MutationDenied(config.MutationWrite) {
		t.Fatalf("setup mutation policy = %v", settings.DeniedMutations)
	}
	if configuration.saveCalls != 1 {
		t.Fatalf("setup saves = %d, want one aggregate save", configuration.saveCalls)
	}
}

func TestConfigSetupKeepsExistingValuesWithoutSaving(t *testing.T) {
	configuration := &fakeConfigStore{}
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-configured", Source: auth.SourceKeychain}}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration

	code, stdout, stderr := runIsolatedWithInput(t, deps, "\n", "config", "setup")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Preferences unchanged.") || configuration.saveCalls != 0 {
		t.Fatalf("unchanged setup = code %d stdout %q stderr %q saves %d", code, stdout, stderr, configuration.saveCalls)
	}
}

func TestConfigSetupReusesAuthJourneyAndRejectsJSON(t *testing.T) {
	missing := &fakeCredentialStore{getErr: errors.New("missing")}
	code, stdout, stderr := runIsolatedWithInput(t, isolatedDependencies(missing), "", "config", "setup")
	if code != 130 || stderr != "Operation interrupted.\n" || !strings.Contains(stdout, "No token configured. Let's set one up.") {
		t.Fatalf("missing auth setup = code %d stdout %q stderr %q", code, stdout, stderr)
	}

	code, stdout, stderr = runIsolated(t, forbiddenDependencies(t), context.Background(), "--json", "config", "setup")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "interactive setup does not support --json") {
		t.Fatalf("JSON setup = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestConfigDisconnectAndSaveFailureAreSemantic(t *testing.T) {
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-env", Source: auth.SourceEnv}}
	code, stdout, stderr := runIsolated(t, isolatedDependencies(credentials), context.Background(), "config", "disconnect")
	if code != 0 || stderr != "" || credentials.clearCalls != 1 || !strings.Contains(stdout, "SLACK_TOKEN remains active") {
		t.Fatalf("disconnect = code %d stdout %q stderr %q", code, stdout, stderr)
	}

	configuration := &fakeConfigStore{saveErr: errors.New("disk full")}
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = configuration
	code, stdout, stderr = runIsolated(t, deps, context.Background(), "config", "disable")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "could not save its configuration") {
		t.Fatalf("save failure = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestEveryConfigSubcommandHelpIsIsolated(t *testing.T) {
	for _, args := range [][]string{
		{"config", "allow", "--help"},
		{"config", "deny", "--help"},
		{"config", "formatting", "--help"},
		{"config", "path", "--help"},
		{"config", "set", "--help"},
		{"config", "setup", "--help"},
	} {
		code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), args...)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage:") {
			t.Fatalf("help %v = code %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}
