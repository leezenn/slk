package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
)

func runIsolatedWithInput(t *testing.T, deps Dependencies, input string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(deps, context.Background(), args, strings.NewReader(input), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestConfigSummaryIsReadOnlyAndSecretSafe(t *testing.T) {
	prefix := "Reviewed by operator."
	configuration := &fakeConfigStore{
		path: "/test/slk/config.json",
		document: config.Document{
			MessagePrefix:   &prefix,
			DeniedMutations: []config.Mutation{config.MutationWrite},
		},
	}
	credentials := &fakeCredentialStore{
		getResult: auth.Result{Token: "xoxp-secret-value", Source: auth.SourceKeychain},
	}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "config")
	if code != 0 || stderr != "" {
		t.Fatalf("config = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"Path: /test/slk/config.json",
		"Tool: enabled",
		"Authentication: configured (keychain, xoxp-sec...)",
		`Message prefix (custom): "Reviewed by operator."`,
		"Denied mutations: write",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("config summary omitted %q: %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "xoxp-secret-value") {
		t.Fatalf("config summary exposed token: %q", stdout)
	}
	if configuration.saveCalls != 0 {
		t.Fatalf("bare config saved %d times, want zero", configuration.saveCalls)
	}
}

func TestConfigSummaryJSONOmitsTokenMaterial(t *testing.T) {
	credentials := &fakeCredentialStore{
		getResult: auth.Result{Token: "xoxp-secret-value", Source: auth.SourceKeychain},
	}
	code, stdout, stderr := runIsolated(t, isolatedDependencies(credentials), context.Background(), "--json", "config")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"auth_configured": true`) || !strings.Contains(stdout, `"auth_source": "keychain"`) {
		t.Fatalf("config JSON = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, forbidden := range []string{`"token":`, `"token_preview":`, "xoxp-secret-value", "xoxp-sec..."} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("config JSON exposed %q: %q", forbidden, stdout)
		}
	}
}

func TestEveryConfigSubcommandHelpIsIsolated(t *testing.T) {
	for _, name := range []string{"allow", "deny", "disable", "disconnect", "enable", "path", "reset", "set", "setup"} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), "config", name, "--help")
			if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage:") {
				t.Fatalf("config %s help = code %d stdout %q stderr %q", name, code, stdout, stderr)
			}
		})
	}
}

func TestConfigPathSupportsJSON(t *testing.T) {
	configuration := &fakeConfigStore{path: "/test/slk/config.json"}
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = configuration

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--json", "config", "path")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"path": "/test/slk/config.json"`) {
		t.Fatalf("config path = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestConfigMessagePrefixSetEmptyAndReset(t *testing.T) {
	configuration := &fakeConfigStore{}
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = configuration

	code, _, stderr := runIsolated(t, deps, context.Background(), "config", "set", "message-prefix", "Custom context")
	if code != 0 || stderr != "" || configuration.document.MessagePrefix == nil || *configuration.document.MessagePrefix != "Custom context" {
		t.Fatalf("set prefix = code %d stderr %q document %#v", code, stderr, configuration.document)
	}
	code, _, stderr = runIsolated(t, deps, context.Background(), "config", "set", "message-prefix", "")
	if code != 0 || stderr != "" || configuration.document.MessagePrefix == nil || *configuration.document.MessagePrefix != "" {
		t.Fatalf("empty prefix = code %d stderr %q document %#v", code, stderr, configuration.document)
	}
	code, _, stderr = runIsolated(t, deps, context.Background(), "config", "reset", "message-prefix")
	if code != 0 || stderr != "" || configuration.document.MessagePrefix != nil {
		t.Fatalf("reset prefix = code %d stderr %q document %#v", code, stderr, configuration.document)
	}
	if configuration.saveCalls != 3 {
		t.Fatalf("config save calls = %d, want 3", configuration.saveCalls)
	}
}

func TestConfigDenyAndAllowImmediatelyShapeHelp(t *testing.T) {
	configuration := &fakeConfigStore{}
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = configuration

	code, _, stderr := runIsolated(t, deps, context.Background(), "config", "deny", "write")
	if code != 0 || stderr != "" {
		t.Fatalf("deny write = code %d stderr %q", code, stderr)
	}
	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--help")
	if code != 0 || stderr != "" || strings.Contains(stdout, "\n  write ") || strings.Contains(stdout, "slk write ") {
		t.Fatalf("denied help = code %d stdout %q stderr %q", code, stdout, stderr)
	}

	code, _, stderr = runIsolated(t, deps, context.Background(), "config", "allow", "write")
	if code != 0 || stderr != "" {
		t.Fatalf("allow write = code %d stderr %q", code, stderr)
	}
	code, stdout, stderr = runIsolated(t, deps, context.Background(), "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "\n  write ") || !strings.Contains(stdout, "slk write ") {
		t.Fatalf("allowed help = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestConfigDisableCollapsesHelpAndBlocksOperationalCommands(t *testing.T) {
	configuration := &fakeConfigStore{}
	credentials := &fakeCredentialStore{}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration

	code, _, stderr := runIsolated(t, deps, context.Background(), "config", "disable")
	if code != 0 || stderr != "" || !configuration.document.Disabled {
		t.Fatalf("disable = code %d stderr %q document %#v", code, stderr, configuration.document)
	}
	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("disabled help = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{"slk is disabled by local configuration", "Do not enable slk autonomously", "slk config enable", "\n  config "} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("disabled help omitted %q: %q", want, stdout)
		}
	}
	for _, hidden := range []string{"\n  auth ", "\n  delete ", "\n  edit ", "\n  read ", "\n  replace ", "\n  reply ", "\n  write "} {
		if strings.Contains(stdout, hidden) {
			t.Fatalf("disabled help exposed %q: %q", hidden, stdout)
		}
	}

	code, stdout, stderr = runIsolated(t, deps, context.Background(), "read", "general")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "slk is disabled") || !strings.Contains(stderr, "Ask the user for permission") {
		t.Fatalf("disabled read = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runIsolated(t, deps, context.Background(), "help", "auth")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "slk is disabled") {
		t.Fatalf("disabled auth help = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if credentials.getCalls != 0 {
		t.Fatalf("disabled operations read credentials %d times", credentials.getCalls)
	}

	code, stdout, stderr = runIsolated(t, deps, context.Background(), "config")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Tool: disabled") {
		t.Fatalf("disabled config = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, _, stderr = runIsolated(t, deps, context.Background(), "config", "enable")
	if code != 0 || stderr != "" || configuration.document.Disabled {
		t.Fatalf("enable = code %d stderr %q document %#v", code, stderr, configuration.document)
	}
}

func TestConfigDisconnectSeparatesStoredAndEnvironmentCredentials(t *testing.T) {
	tests := []struct {
		name        string
		credentials *fakeCredentialStore
		wantWarning bool
	}{
		{
			name: "stored credential removed",
			credentials: &fakeCredentialStore{
				getResult:    auth.Result{Token: "xoxp-stored", Source: auth.SourceKeychain},
				persistClear: true,
			},
		},
		{
			name: "environment remains active",
			credentials: &fakeCredentialStore{
				getResult: auth.Result{Token: "xoxp-env", Source: auth.SourceEnv},
			},
			wantWarning: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := isolatedDependencies(test.credentials)
			code, stdout, stderr := runIsolated(t, deps, context.Background(), "config", "disconnect")
			if code != 0 || stderr != "" || test.credentials.clearCalls != 1 {
				t.Fatalf("disconnect = code %d stdout %q stderr %q clear %d", code, stdout, stderr, test.credentials.clearCalls)
			}
			if got := strings.Contains(stdout, "SLACK_TOKEN remains active"); got != test.wantWarning {
				t.Fatalf("environment warning = %v, want %v: %q", got, test.wantWarning, stdout)
			}
		})
	}
}

func TestConfigSetupKeepsExistingAuthAndPreferencesByDefault(t *testing.T) {
	configuration := &fakeConfigStore{document: config.Document{Disabled: true}}
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-configured", Source: auth.SourceKeychain}}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration

	code, stdout, stderr := runIsolatedWithInput(t, deps, "\n", "config", "setup")
	if code != 0 || stderr != "" {
		t.Fatalf("setup = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{"Authentication: configured", "Tool: disabled", "Change preferences? [y/N]", "Preferences unchanged.", "slk remains disabled"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("setup omitted %q: %q", want, stdout)
		}
	}
	if credentials.setCalls != 0 || configuration.saveCalls != 0 {
		t.Fatalf("setup mutated auth/config: set %d save %d", credentials.setCalls, configuration.saveCalls)
	}
}

func TestConfigSetupChangesPreferencesThroughOneInputReader(t *testing.T) {
	configuration := &fakeConfigStore{}
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-configured", Source: auth.SourceKeychain}}
	deps := isolatedDependencies(credentials)
	deps.Configuration = configuration

	input := "y\ny\nCustom context\nn\n\n\n\nn\n"
	code, stdout, stderr := runIsolatedWithInput(t, deps, input, "config", "setup")
	if code != 0 || stderr != "" {
		t.Fatalf("setup = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if configuration.document.MessagePrefix == nil || *configuration.document.MessagePrefix != "Custom context" {
		t.Fatalf("setup prefix = %#v", configuration.document.MessagePrefix)
	}
	settings := configuration.document.Effective()
	if !settings.MutationDenied(config.MutationReply) ||
		settings.MutationDenied(config.MutationWrite) ||
		settings.MutationDenied(config.MutationEdit) ||
		settings.MutationDenied(config.MutationReplace) ||
		!settings.MutationDenied(config.MutationDelete) {
		t.Fatalf("setup denied mutations = %v", settings.DeniedMutations)
	}
	if configuration.saveCalls != 1 || !strings.Contains(stdout, "Preferences saved") {
		t.Fatalf("setup save = calls %d stdout %q", configuration.saveCalls, stdout)
	}
}

func TestConfigSetupMissingAuthUsesExistingJourney(t *testing.T) {
	credentials := &fakeCredentialStore{getErr: errors.New("missing")}
	deps := isolatedDependencies(credentials)

	code, stdout, stderr := runIsolatedWithInput(t, deps, "", "config", "setup")
	if code != 130 || stderr != "Operation interrupted.\n" || !strings.Contains(stdout, "No token configured. Let's set one up.") {
		t.Fatalf("missing auth setup = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestConfigSetupReconnectUsesExistingAuthJourney(t *testing.T) {
	credentials := &fakeCredentialStore{getResult: auth.Result{Token: "xoxp-configured", Source: auth.SourceKeychain}}
	deps := isolatedDependencies(credentials)

	code, stdout, stderr := runIsolatedWithInput(t, deps, "", "config", "setup", "--reconnect")
	if code != 130 || stderr != "Operation interrupted.\n" || !strings.Contains(stdout, "Let's reconnect Slack with a new token.") {
		t.Fatalf("reconnect = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestConfigSetupRejectsJSONBeforeDependencies(t *testing.T) {
	code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), "--json", "config", "setup")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "interactive setup does not support --json") {
		t.Fatalf("JSON setup = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestConfigSaveFailureIsSemantic(t *testing.T) {
	configuration := &fakeConfigStore{saveErr: errors.New("disk full")}
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = configuration

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "config", "disable")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "could not save its configuration") || !strings.Contains(stderr, "permissions") {
		t.Fatalf("save failure = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}
