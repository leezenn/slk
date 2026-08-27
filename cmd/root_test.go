package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
)

type fakeConfigStore struct {
	document  config.Document
	loadErr   error
	saveErr   error
	pathErr   error
	path      string
	loadCalls int
	saveCalls int
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

func (f *fakeConfigStore) Load() (config.Settings, error) {
	f.loadCalls++
	if f.loadErr != nil {
		return config.Settings{}, f.loadErr
	}
	return f.document.Effective(), nil
}

func (f *fakeConfigStore) LoadDocument() (config.Document, error) {
	f.loadCalls++
	if f.loadErr != nil {
		return config.Document{}, f.loadErr
	}
	return f.document, nil
}

func (f *fakeConfigStore) Save(document config.Document) error {
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.document = document
	return nil
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

func (f *forbiddenConfigStore) Load() (config.Settings, error) {
	f.t.Fatal("test reached the config load seam")
	return config.Settings{}, nil
}

func (f *forbiddenConfigStore) LoadDocument() (config.Document, error) {
	f.t.Fatal("test reached the config document seam")
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
	"activity", "auth", "channels", "config", "download", "members", "notes", "open",
	"read", "recent", "reply", "search", "thread", "users", "whoami", "write",
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
		{name: "download shape", args: []string{"download"}},
		{name: "members shape", args: []string{"members"}},
		{name: "notes shape", args: []string{"notes", "extra"}},
		{name: "open shape", args: []string{"open"}},
		{name: "read shape", args: []string{"read"}},
		{name: "recent shape", args: []string{"recent", "extra"}},
		{name: "recent limit", args: []string{"recent", "--limit", "0"}},
		{name: "recent type", args: []string{"recent", "--type", "unknown"}},
		{name: "recent time", args: []string{"recent", "--since", "not-time"}},
		{name: "reply shape", args: []string{"reply"}},
		{name: "reply text", args: []string{"reply", "https://workspace.slack.com/archives/C12345678/p1705312325000100"}},
		{name: "reply permalink", args: []string{"reply", "not-a-permalink", "--text", "hello"}},
		{name: "write shape", args: []string{"write"}},
		{name: "write text", args: []string{"write", "general"}},
		{name: "write target", args: []string{"write", "", "--text", "hello"}},
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
		{"notes"}, {"open", "https://workspace.slack.com/archives/C12345678/p1705312325000100"},
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
