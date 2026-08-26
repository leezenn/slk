package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileAppliesNoticeSemantics(t *testing.T) {
	tests := []struct {
		name    string
		content *string
		want    string
		wantErr string
	}{
		{name: "missing file", want: DefaultReplyPrefix},
		{name: "missing key", content: stringPointer(`{}`), want: DefaultReplyPrefix},
		{name: "override", content: stringPointer(`{"reply_prefix":"Reviewed by the operator."}`), want: "Reviewed by the operator."},
		{name: "explicit empty disables", content: stringPointer(`{"reply_prefix":""}`), want: ""},
		{name: "whitespace only rejected", content: stringPointer(`{"reply_prefix":"   "}`), wantErr: "must be empty or contain visible text"},
		{name: "wrong type rejected", content: stringPointer(`{"reply_prefix":false}`), wantErr: "cannot unmarshal"},
		{name: "over-specific key rejected", content: stringPointer(`{"agent_assisted_prefix":"hello"}`), wantErr: "unknown field"},
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
			if settings.ReplyPrefix != test.want {
				t.Fatalf("prefix = %q, want %q", settings.ReplyPrefix, test.want)
			}
		})
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
