package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
)

type fakeFileInfoGetter struct {
	file  *api.File
	err   error
	gotID string
}

func (f *fakeFileInfoGetter) GetFileInfo(fileID string) (*api.File, error) {
	f.gotID = fileID
	return f.file, f.err
}

func TestResolveDownloadSourceFromFileID(t *testing.T) {
	getter := &fakeFileInfoGetter{file: &api.File{
		ID:                 "F12345678",
		Name:               "../report.pdf",
		URLPrivate:         "https://files.slack.com/view/report.pdf",
		URLPrivateDownload: "https://files.slack.com/download/report.pdf",
	}}

	got, err := resolveDownloadSource(getter, "F12345678")
	if err != nil {
		t.Fatalf("resolveDownloadSource returned error: %v", err)
	}
	if getter.gotID != "F12345678" {
		t.Fatalf("files.info called with %q, want F12345678", getter.gotID)
	}
	if got.URL != "https://files.slack.com/download/report.pdf" {
		t.Fatalf("URL = %q, want download URL", got.URL)
	}
	if got.Filename != "report.pdf" {
		t.Fatalf("Filename = %q, want sanitized report.pdf", got.Filename)
	}
}

func TestResolveDownloadSourceFallsBackToPrivateURL(t *testing.T) {
	getter := &fakeFileInfoGetter{file: &api.File{
		ID:         "F12345678",
		Name:       "report.pdf",
		URLPrivate: "https://files.slack.com/view/report.pdf",
	}}

	got, err := resolveDownloadSource(getter, "F12345678")
	if err != nil {
		t.Fatalf("resolveDownloadSource returned error: %v", err)
	}
	if got.URL != "https://files.slack.com/view/report.pdf" {
		t.Fatalf("URL = %q, want private URL fallback", got.URL)
	}
}

func TestResolveDownloadSourceRejectsMissingPrivateURL(t *testing.T) {
	getter := &fakeFileInfoGetter{file: &api.File{ID: "F12345678", Name: "report.pdf"}}

	_, err := resolveDownloadSource(getter, "F12345678")
	if err == nil || !strings.Contains(err.Error(), "has no private download URL") {
		t.Fatalf("error = %v, want missing private download URL error", err)
	}
}

func TestResolveDownloadSourcePropagatesLookupError(t *testing.T) {
	getter := &fakeFileInfoGetter{err: errors.New("lookup failed")}

	_, err := resolveDownloadSource(getter, "F12345678")
	if err == nil || !strings.Contains(err.Error(), "resolving Slack file F12345678") {
		t.Fatalf("error = %v, want contextual lookup error", err)
	}
}

func TestResolveDownloadSourceRejectsURLInput(t *testing.T) {
	const fileURL = "https://files.slack.com/files-pri/T123-F123/report.pdf"

	_, err := resolveDownloadSource(nil, fileURL)
	if err == nil || !strings.Contains(err.Error(), "expected an ID like F0123456789") {
		t.Fatalf("error = %v, want file-ID guidance", err)
	}
	if strings.Contains(err.Error(), fileURL) || strings.Contains(err.Error(), "files.slack.com") {
		t.Fatalf("error leaked rejected URL input: %v", err)
	}
}

func TestFormatDownloadResultHasSemanticParity(t *testing.T) {
	want := downloadResult{FileID: "F12345678", Path: "report.pdf", Bytes: 42}

	text, err := formatDownloadResult(want, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{want.FileID, want.Path, "42 bytes"} {
		if !strings.Contains(text, value) {
			t.Fatalf("default output omitted %q: %s", value, text)
		}
	}

	rawJSON, err := formatDownloadResult(want, true)
	if err != nil {
		t.Fatal(err)
	}
	var got downloadJSONOutput
	if err := json.Unmarshal([]byte(rawJSON), &got); err != nil {
		t.Fatalf("JSON output is invalid: %v\n%s", err, rawJSON)
	}
	if !got.OK || got.Download != want {
		t.Fatalf("JSON output = %+v, want ok with download %+v", got, want)
	}
}

type failingReader struct {
	sent bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("read failed")
}

func TestWriteDownloadFileRefusesOverwriteByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ensureDownloadDestinationAvailable(path, false); err == nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("preflight error = %v, want existing-file error", err)
	}
	_, err := writeDownloadFile(path, false, strings.NewReader("replacement"))
	if err == nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("write error = %v, want existing-file error", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original" {
		t.Fatalf("existing contents changed to %q", contents)
	}
}

func TestWriteDownloadFileCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")

	if _, err := writeDownloadFile(path, false, strings.NewReader("contents")); err != nil {
		t.Fatalf("writeDownloadFile returned error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("new download mode = %o, want 600", got)
	}
}

func TestWriteDownloadFileOverwritesOnlyAfterSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	written, err := writeDownloadFile(path, true, strings.NewReader("replacement"))
	if err != nil {
		t.Fatalf("writeDownloadFile returned error: %v", err)
	}
	if written != int64(len("replacement")) {
		t.Fatalf("written = %d, want %d", written, len("replacement"))
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "replacement" {
		t.Fatalf("contents = %q, want replacement", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("forced replacement mode = %o, want preserved mode 600", got)
	}
}

func TestWriteDownloadFileRemovesPartialFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.pdf")

	_, err := writeDownloadFile(path, false, &failingReader{})
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("error = %v, want read failure", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial destination exists after failure: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain after failure: %v", entries)
	}
}

func TestWriteDownloadFilePreservesForcedTargetOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := writeDownloadFile(path, true, &failingReader{})
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("error = %v, want read failure", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original" {
		t.Fatalf("forced target changed after failed download: %q", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("failed forced target mode = %o, want 600", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "report.pdf" {
		t.Fatalf("unexpected files after failure: %v", entries)
	}
}
