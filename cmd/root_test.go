package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
	"github.com/leezenn/slk/internal/presentation"
	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

type fakeConfigStore struct {
	document          config.Document
	identityDocuments map[string]config.Preferences
	loadErr           error
	saveErr           error
	pathErr           error
	path              string
	loadCalls         int
	saveCalls         int
}

func (f *fakeConfigStore) Path() (string, error) {
	if f.pathErr != nil {
		return "", f.pathErr
	}
	if f.path == "" {
		return "/test/config.json", nil
	}
	return f.path, nil
}

func (f *fakeConfigStore) Load() (config.Document, error) {
	f.loadCalls++
	if f.loadErr != nil {
		return config.Document{}, f.loadErr
	}
	document := cloneConfigDocument(f.document)
	if len(f.identityDocuments) != 0 {
		document.Identities = cloneIdentityPreferences(f.identityDocuments)
	}
	return document, nil
}

func (f *fakeConfigStore) Save(document config.Document) error {
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.document = cloneConfigDocument(document)
	f.identityDocuments = cloneIdentityPreferences(document.Identities)
	return nil
}

func cloneConfigDocument(document config.Document) config.Document {
	document.DeniedMutations = append([]config.Mutation(nil), document.DeniedMutations...)
	document.Identities = cloneIdentityPreferences(document.Identities)
	return document
}

func cloneIdentityPreferences(values map[string]config.Preferences) map[string]config.Preferences {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]config.Preferences, len(values))
	for namespace, preferences := range values {
		preferences.Formatting = append([]textformat.Module(nil), preferences.Formatting...)
		cloned[namespace] = preferences
	}
	return cloned
}

func syntheticIdentity() config.Identity {
	identity, err := config.NewIdentity("T-SYNTHETIC", "U-SYNTHETIC")
	if err != nil {
		panic(err)
	}
	return identity
}

func (f *fakeConfigStore) syntheticIdentityDocument() config.Preferences {
	namespace, _ := syntheticIdentity().Namespace()
	return f.identityDocuments[namespace]
}

func syntheticIdentityDocuments(preferences config.Preferences) map[string]config.Preferences {
	namespace, _ := syntheticIdentity().Namespace()
	return map[string]config.Preferences{namespace: preferences}
}

type fakeCredentialStore struct {
	getResult    auth.Result
	getErr       error
	getCalls     int
	setCalls     int
	clearCalls   int
	persistClear bool
}

func (f *fakeCredentialStore) Get() (auth.Result, error) {
	f.getCalls++
	return f.getResult, f.getErr
}

func (f *fakeCredentialStore) Set(token string) error {
	f.setCalls++
	f.getResult = auth.Result{Token: token, Source: auth.SourceKeychain}
	f.getErr = nil
	return nil
}

func (f *fakeCredentialStore) Clear() error {
	f.clearCalls++
	if f.persistClear {
		f.getResult = auth.Result{Source: auth.SourceNone}
		f.getErr = errors.New("missing")
	}
	return nil
}

func isolatedDependencies(store auth.Store) Dependencies {
	return Dependencies{
		Credentials:   store,
		Configuration: &fakeConfigStore{},
		NewClient: func(string) *api.Client {
			panic("Slack client factory must not be called")
		},
		ValidateToken: func(context.Context, string, io.Writer) (*api.AuthTestResult, error) {
			return &api.AuthTestResult{TeamID: "T-SYNTHETIC", UserID: "U-SYNTHETIC", User: "owner", Team: "Test Workspace"}, nil
		},
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
}

func forbiddenDependencies(t *testing.T) Dependencies {
	t.Helper()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	t.Setenv("PATH", filepath.Join(t.TempDir(), "no-executables"))
	t.Setenv("SLACK_TOKEN", "")

	return Dependencies{
		Credentials:   &forbiddenCredentialStore{t: t},
		Configuration: &fakeConfigStore{},
		NewClient: func(string) *api.Client {
			t.Fatal("test reached the live Slack client seam")
			return nil
		},
		ValidateToken: func(context.Context, string, io.Writer) (*api.AuthTestResult, error) {
			t.Fatal("test reached Slack identity validation")
			return nil, nil
		},
		Now: func() time.Time {
			t.Fatal("test reached the clock before cancellation")
			return time.Time{}
		},
	}
}

type forbiddenConfigStore struct{ t *testing.T }

func (f *forbiddenConfigStore) Path() (string, error) {
	f.t.Fatal("test reached the config path seam")
	return "", nil
}

func (f *forbiddenConfigStore) Load() (config.Document, error) {
	f.t.Fatal("test reached the config load seam")
	return config.Document{}, nil
}

func (f *forbiddenConfigStore) Save(config.Document) error {
	f.t.Fatal("test reached the config save seam")
	return nil
}

type forbiddenCredentialStore struct{ t *testing.T }

func (f *forbiddenCredentialStore) Get() (auth.Result, error) {
	f.t.Fatal("test reached the live credential seam")
	return auth.Result{}, nil
}

func (f *forbiddenCredentialStore) Set(string) error {
	f.t.Fatal("test reached the live credential seam")
	return nil
}

func (f *forbiddenCredentialStore) Clear() error {
	f.t.Fatal("test reached the live credential seam")
	return nil
}

func runIsolated(t *testing.T, deps Dependencies, ctx context.Context, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(deps, ctx, args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

var commandNames = []string{
	"activity", "auth", "channels", "config", "delete", "download", "edit", "members", "open",
	"read", "recent", "replace", "reply", "search", "style", "thread", "users", "whoami", "write",
}

func TestNewRootCommandBuildsFreshRunECommands(t *testing.T) {
	root := NewRootCommand(Dependencies{})
	other := NewRootCommand(Dependencies{})
	if root == other {
		t.Fatal("NewRootCommand returned the same root pointer")
	}
	if !root.SilenceErrors || !root.SilenceUsage {
		t.Fatal("root must render errors at one boundary")
	}

	for _, name := range commandNames {
		command, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("finding %q: %v", name, err)
		}
		otherCommand, _, err := other.Find([]string{name})
		if err != nil {
			t.Fatalf("finding %q in second tree: %v", name, err)
		}
		if command == otherCommand {
			t.Errorf("reused %q command pointer", name)
		}
		if command.RunE == nil || command.Run != nil {
			t.Errorf("%q must use RunE only", name)
		}
	}
}

func TestRepeatedAuthExecutionDoesNotLeakFlags(t *testing.T) {
	store := &fakeCredentialStore{getResult: auth.Result{Token: "test", Source: auth.SourceEnv}}
	deps := isolatedDependencies(store)

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "auth", "--clear")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Token removed") {
		t.Fatalf("clear = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runIsolated(t, deps, context.Background(), "auth")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Status: configured") {
		t.Fatalf("status = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if store.clearCalls != 1 || store.getCalls != 1 {
		t.Fatalf("store calls = clear:%d get:%d, want 1 each", store.clearCalls, store.getCalls)
	}
}

func TestBareAuthGuidesWhenCredentialIsMissing(t *testing.T) {
	store := &fakeCredentialStore{getErr: errors.New("missing")}
	code, stdout, stderr := runIsolated(t, isolatedDependencies(store), context.Background(), "auth")
	if code != 130 || stderr != "Operation interrupted.\n" {
		t.Fatalf("guided auth = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, guidance := range []string{"No token configured. Let's set one up.", "For non-interactive use: slk auth <token>"} {
		if !strings.Contains(stdout, guidance) {
			t.Errorf("guided auth missing %q in %q", guidance, stdout)
		}
	}
}

func TestEveryCommandHelpIsIsolated(t *testing.T) {
	tests := append([]struct {
		name string
		args []string
	}{{name: "root", args: []string{"--help"}}}, make([]struct {
		name string
		args []string
	}, len(commandNames))...)
	for index, name := range commandNames {
		tests[index+1] = struct {
			name string
			args []string
		}{name: name, args: []string{name, "--help"}}
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), test.args...)
			if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage:") {
				t.Fatalf("help = code %d stdout %q stderr %q", code, stdout, stderr)
			}
		})
	}
}

func TestJSONHelpLabelsSupportBoundary(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "root", args: []string{"--help"}, want: "Output structured success data where supported"},
		{name: "auth", args: []string{"auth", "--help"}, want: "Authentication output is semantic text; the inherited --json flag has no effect."},
		{name: "setup", args: []string{"config", "setup", "--help"}, want: "Setup does not support the inherited --json flag."},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), test.args...)
			if code != 0 || stderr != "" || !strings.Contains(stdout, test.want) {
				t.Fatalf("help = code %d stdout %q stderr %q, want %q", code, stdout, stderr, test.want)
			}
		})
	}
}

func TestAuthJSONFlagUsesSemanticText(t *testing.T) {
	store := &fakeCredentialStore{getResult: auth.Result{Token: "test", Source: auth.SourceEnv}}
	code, stdout, stderr := runIsolated(t, isolatedDependencies(store), context.Background(), "--json", "auth")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Status: configured") || strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Fatalf("auth --json = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if store.getCalls != 1 {
		t.Fatalf("credential store Get calls = %d, want one", store.getCalls)
	}
}

func TestRemovedNotesCommandIsUnavailable(t *testing.T) {
	store := &fakeCredentialStore{}
	deps := isolatedDependencies(store)

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("root help = code %d stderr %q", code, stderr)
	}
	if strings.Contains(stdout, "\n  notes ") {
		t.Fatalf("root help still exposes notes: %q", stdout)
	}

	code, stdout, stderr = runIsolated(t, deps, context.Background(), "notes")
	if code != 1 || stdout != "" || !strings.Contains(stderr, `unknown command "notes"`) {
		t.Fatalf("notes invocation = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if store.getCalls != 0 {
		t.Fatalf("credential store Get calls = %d, want zero", store.getCalls)
	}
}

func TestBindCommandIdentityLoadsValidatedIdentityPreferences(t *testing.T) {
	prefix := "Identity prefix"
	mode := presentation.AlwaysExpanded
	preferences := config.Preferences{
		MessagePrefix:       &prefix,
		MessagePresentation: &mode,
		Formatting:          []textformat.Module{textformat.ModuleEmDashToSpacedHyphen},
	}
	deps := isolatedDependencies(&fakeCredentialStore{getResult: auth.Result{Token: "xoxp-synthetic", Source: auth.SourceKeychain}})
	deps.Configuration = &fakeConfigStore{identityDocuments: syntheticIdentityDocuments(preferences)}
	command := &cobra.Command{Use: "test"}
	command.SetContext(context.Background())
	command.SetErr(&bytes.Buffer{})

	bound, settings, err := bindCommandIdentity(command, deps)
	if err != nil {
		t.Fatal(err)
	}
	if bound.ActiveIdentity == nil || bound.ActiveToken != "xoxp-synthetic" || settings.MessagePrefix != prefix ||
		settings.MessagePresentation != mode || !settings.FormattingEnabled(textformat.ModuleEmDashToSpacedHyphen) {
		t.Fatalf("bound identity settings = %#v, %#v", bound, settings)
	}
}

func TestPresentationHelpRemainsOfflineAndOmitsIdentityPreference(t *testing.T) {
	mode := presentation.AlwaysExpanded
	deps := isolatedDependencies(&fakeCredentialStore{})
	deps.Configuration = &fakeConfigStore{identityDocuments: syntheticIdentityDocuments(config.Preferences{MessagePresentation: &mode})}

	for _, args := range [][]string{{"--help"}, {"write", "--help"}, {"reply", "--help"}, {"replace", "--help"}} {
		code, stdout, stderr := runIsolated(t, deps, context.Background(), args...)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "Built-in default: slack-managed") ||
			!strings.Contains(stdout, "Authenticated identity preferences are applied at execution") ||
			strings.Contains(stdout, "Built-in default: always-expanded") || !strings.Contains(stdout, "--presentation") {
			t.Fatalf("presentation help %v = code %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "edit", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "preserves the target's normalized message presentation") ||
		strings.Contains(stdout, "--presentation string") {
		t.Fatalf("edit presentation help = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestRootDistinguishesEditFromReplace(t *testing.T) {
	code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "\n  edit ") || !strings.Contains(stdout, "\n  replace ") {
		t.Fatalf("root help = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "slk edit '<slack-permalink>' --match") || !strings.Contains(stdout, "slk replace '<slack-permalink>' --text") {
		t.Fatalf("root examples do not distinguish edit/replace: %q", stdout)
	}
}

func TestInvalidInputFailsBeforeDependencies(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "activity shape", args: []string{"activity", "one", "two"}},
		{name: "activity limit", args: []string{"activity", "--limit", "101"}},
		{name: "activity time", args: []string{"activity", "--since", "not-time"}},
		{name: "auth shape", args: []string{"auth", "one", "two"}},
		{name: "interactive auth token", args: []string{"auth", "--interactive", "xoxp-placeholder"}},
		{name: "interactive auth clear", args: []string{"auth", "--interactive", "--clear"}},
		{name: "invalid auth token", args: []string{"auth", "xoxb-not-supported"}},
		{name: "channels shape", args: []string{"channels", "extra"}},
		{name: "delete shape", args: []string{"delete"}},
		{name: "delete permalink", args: []string{"delete", "not-a-permalink", "--yes"}},
		{name: "download shape", args: []string{"download"}},
		{name: "edit shape", args: []string{"edit"}},
		{name: "edit match", args: []string{"edit", "https://workspace.slack.com/archives/C12345678/p1705312325000100", "--with", "replacement"}},
		{name: "edit replacement", args: []string{"edit", "https://workspace.slack.com/archives/C12345678/p1705312325000100", "--match", "current"}},
		{name: "edit permalink", args: []string{"edit", "not-a-permalink", "--match", "current", "--with", "replacement"}},
		{name: "members shape", args: []string{"members"}},
		{name: "open shape", args: []string{"open"}},
		{name: "read shape", args: []string{"read"}},
		{name: "recent shape", args: []string{"recent", "extra"}},
		{name: "recent limit", args: []string{"recent", "--limit", "0"}},
		{name: "recent type", args: []string{"recent", "--type", "unknown"}},
		{name: "recent time", args: []string{"recent", "--since", "not-time"}},
		{name: "replace shape", args: []string{"replace"}},
		{name: "replace text", args: []string{"replace", "https://workspace.slack.com/archives/C12345678/p1705312325000100"}},
		{name: "replace permalink", args: []string{"replace", "not-a-permalink", "--text", "complete replacement"}},
		{name: "replace presentation", args: []string{"replace", "https://workspace.slack.com/archives/C12345678/p1705312325000100", "--text", "complete replacement", "--presentation", "forced"}},
		{name: "reply shape", args: []string{"reply"}},
		{name: "reply text", args: []string{"reply", "https://workspace.slack.com/archives/C12345678/p1705312325000100"}},
		{name: "reply permalink", args: []string{"reply", "not-a-permalink", "--text", "hello"}},
		{name: "reply presentation", args: []string{"reply", "https://workspace.slack.com/archives/C12345678/p1705312325000100", "--text", "hello", "--presentation", "forced"}},
		{name: "write shape", args: []string{"write"}},
		{name: "write text", args: []string{"write", "general"}},
		{name: "write target", args: []string{"write", "", "--text", "hello"}},
		{name: "write presentation", args: []string{"write", "general", "--text", "hello", "--presentation", "forced"}},
		{name: "edit presentation override", args: []string{"edit", "https://workspace.slack.com/archives/C12345678/p1705312325000100", "--match", "old", "--with", "new", "--presentation", "always-expanded"}},
		{name: "search shape", args: []string{"search"}},
		{name: "thread shape", args: []string{"thread", "general"}},
		{name: "users shape", args: []string{"users", "one", "two"}},
		{name: "whoami shape", args: []string{"whoami", "extra"}},
		{name: "unknown flag", args: []string{"channels", "--not-a-flag"}},
		{name: "malformed integer", args: []string{"read", "general", "--limit", "not-an-integer"}},
		{name: "invalid file ID", args: []string{"download", "not-a-file"}},
		{name: "invalid permalink", args: []string{"open", "not-a-permalink"}},
		{name: "conflicting read modes", args: []string{"read", "general", "--around", "1.0", "--after", "1d"}},
		{name: "invalid time", args: []string{"read", "general", "--after", "not-time"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeCredentialStore{}
			code, stdout, stderr := runIsolated(t, isolatedDependencies(store), context.Background(), test.args...)
			if code != 1 || stdout != "" {
				t.Fatalf("result = code %d stdout %q stderr %q", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "--help") || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("error does not guide recovery: %q", stderr)
			}
			if store.getCalls != 0 || store.setCalls != 0 || store.clearCalls != 0 {
				t.Fatalf("credential dependency called: %+v", store)
			}
		})
	}
}

func TestDeniedMutationsShapeHelpAndRefuseInvocation(t *testing.T) {
	tests := []struct {
		mutation config.Mutation
		other    config.Mutation
		args     []string
	}{
		{
			mutation: config.MutationDelete,
			other:    config.MutationWrite,
			args: []string{
				"delete",
				"https://workspace.slack.com/archives/C12345678/p1705312325000100",
				"--yes",
			},
		},
		{
			mutation: config.MutationEdit,
			other:    config.MutationWrite,
			args: []string{
				"edit",
				"https://workspace.slack.com/archives/C12345678/p1705312325000100",
				"--match",
				"current",
				"--with",
				"replacement",
			},
		},
		{
			mutation: config.MutationReplace,
			other:    config.MutationWrite,
			args: []string{
				"replace",
				"https://workspace.slack.com/archives/C12345678/p1705312325000100",
				"--text",
				"complete replacement",
			},
		},
		{
			mutation: config.MutationReply,
			other:    config.MutationWrite,
			args: []string{
				"reply",
				"https://workspace.slack.com/archives/C12345678/p1705312325000100",
				"--text",
				"hello",
			},
		},
		{
			mutation: config.MutationWrite,
			other:    config.MutationReply,
			args:     []string{"--json", "write", "--help"},
		},
	}

	for _, test := range tests {
		t.Run(string(test.mutation), func(t *testing.T) {
			store := &fakeCredentialStore{}
			deps := isolatedDependencies(store)
			deps.Configuration = &fakeConfigStore{document: config.Document{DeniedMutations: []config.Mutation{test.mutation}}}

			code, stdout, stderr := runIsolated(t, deps, context.Background(), "--help")
			if code != 0 || stderr != "" {
				t.Fatalf("help = code %d stdout %q stderr %q", code, stdout, stderr)
			}
			if strings.Contains(stdout, "\n  "+string(test.mutation)+" ") || strings.Contains(stdout, "slk "+string(test.mutation)+" ") {
				t.Fatalf("help exposed denied mutation %q: %q", test.mutation, stdout)
			}
			if !strings.Contains(stdout, "\n  "+string(test.other)+" ") || !strings.Contains(stdout, "slk "+string(test.other)+" ") {
				t.Fatalf("help omitted allowed mutation %q: %q", test.other, stdout)
			}

			code, stdout, stderr = runIsolated(t, deps, context.Background(), test.args...)
			if code != 1 || stdout != "" || !strings.Contains(stderr, "disabled by configuration") || !strings.Contains(stderr, "deny_mutations") {
				t.Fatalf("denied invocation = code %d stdout %q stderr %q", code, stdout, stderr)
			}
			if strings.Contains(stderr, "Usage:") {
				t.Fatalf("denied invocation rendered confusing usage: %q", stderr)
			}
			code, stdout, stderr = runIsolated(t, deps, context.Background(), "help", string(test.mutation))
			if code != 1 || stdout != "" || !strings.Contains(stderr, "disabled by configuration") {
				t.Fatalf("denied help topic = code %d stdout %q stderr %q", code, stdout, stderr)
			}
			if store.getCalls != 0 {
				t.Fatalf("credential Get calls = %d, want zero", store.getCalls)
			}
		})
	}
}

func TestAuthRequiredExplainsRecovery(t *testing.T) {
	store := &fakeCredentialStore{getErr: errors.New("missing")}
	code, stdout, stderr := runIsolated(t, isolatedDependencies(store), context.Background(), "whoami")
	const want = "Slack authentication is not configured.\nRun 'slk auth' to connect Slack, then retry.\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Fatalf("result = code %d stdout %q stderr %q, want stderr %q", code, stdout, stderr, want)
	}
}

func TestReplyConfigFailureStopsBeforeCredentialAccess(t *testing.T) {
	store := &fakeCredentialStore{}
	deps := isolatedDependencies(store)
	deps.Configuration = &fakeConfigStore{loadErr: errors.New("invalid message_prefix")}

	code, stdout, stderr := runIsolated(
		t,
		deps,
		context.Background(),
		"reply",
		"https://workspace.slack.com/archives/C12345678/p1705312325000100",
		"--text",
		"hello",
	)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "could not load its configuration") || !strings.Contains(stderr, "Fix the configuration file") {
		t.Fatalf("result = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if store.getCalls != 0 {
		t.Fatalf("credential store Get calls = %d, want zero", store.getCalls)
	}
}

func TestCanceledContextStopsEveryCommandBeforeDependencies(t *testing.T) {
	tests := [][]string{
		{"activity"}, {"auth"}, {"channels"}, {"download", "F0123456789"}, {"members", "general"},
		{"open", "https://workspace.slack.com/archives/C12345678/p1705312325000100"},
		{"read", "general"},
		{"recent"},
		{"reply", "https://workspace.slack.com/archives/C12345678/p1705312325000100", "--text", "hello"},
		{"search", "query"}, {"thread", "general", "1705312325.000100"},
		{"users"}, {"whoami"}, {"write", "general", "--text", "hello"},
	}
	for _, args := range tests {
		t.Run(args[0], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			deps := forbiddenDependencies(t)
			deps.Configuration = &forbiddenConfigStore{t: t}
			code, stdout, stderr := runIsolated(t, deps, ctx, args...)
			if code != 130 || stdout != "" || stderr != "Operation interrupted.\n" {
				t.Fatalf("result = code %d stdout %q stderr %q", code, stdout, stderr)
			}
		})
	}
}

func TestErrorDetailsRedactSecretsAndPrivateURLs(t *testing.T) {
	got := safeDynamic("HTTPS://FILES.SLACK.COM/private xoxp-secret-value", 256)
	if got != "[redacted] [redacted]" {
		t.Fatalf("safeDynamic returned %q", got)
	}
}

func TestProcessGlobalsStayAtMainBoundary(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate root_test.go")
	}
	repositoryRoot := filepath.Dir(filepath.Dir(currentFile))
	patterns := []string{"os.Exit(", "os.Stdin", "os.Stdout", "os.Stderr"}
	exitCalls := 0

	err := filepath.Walk(repositoryRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == ".pi" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		for _, pattern := range patterns {
			count := strings.Count(string(contents), pattern)
			if pattern == "os.Exit(" {
				exitCalls += count
			}
			if relative != "main.go" && count > 0 {
				t.Errorf("%s uses process global %s", relative, pattern)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if exitCalls != 1 {
		t.Fatalf("os.Exit call count = %d, want one in main.go", exitCalls)
	}
}
