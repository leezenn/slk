package format

import (
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
)

const (
	testSelfID  = "U12345678"
	testOtherID = "U87654321"
)

func testResolveUser(userID string) string {
	switch userID {
	case testSelfID:
		return "owner"
	case testOtherID:
		return "teammate"
	default:
		return userID
	}
}

func TestFormatMessagesMarksAuthenticatedUser(t *testing.T) {
	messages := []api.Message{
		{
			User: testSelfID,
			Text: "mine",
			Ts:   "1700000000.000000",
			Files: []api.File{
				{ID: "F12345678", Name: "report.pdf", Size: 1024, Mimetype: "application/pdf"},
			},
		},
		{User: testOtherID, Text: "theirs", Ts: "1700000001.000000"},
	}

	got := FormatMessages(messages, "general", testResolveUser, testSelfID)
	if !strings.Contains(got, "@owner (me): mine") {
		t.Fatalf("authenticated author was not marked as me:\n%s", got)
	}
	if !strings.Contains(got, "@teammate: theirs") {
		t.Fatalf("other author was not rendered normally:\n%s", got)
	}
	if strings.Contains(got, "@teammate (me)") {
		t.Fatalf("other author was incorrectly marked as me:\n%s", got)
	}
	if !strings.Contains(got, "[pdf] report.pdf (1.0KB) — slk download F12345678") {
		t.Fatalf("attachment did not include a runnable download command:\n%s", got)
	}
}

func TestFileToJSONExcludesPrivateURLs(t *testing.T) {
	file := api.File{
		ID:                 "F12345678",
		Name:               "report.pdf",
		Size:               1024,
		Mimetype:           "application/pdf",
		Filetype:           "pdf",
		PrettyType:         "PDF",
		URLPrivate:         "https://files.slack.com/private/report.pdf",
		URLPrivateDownload: "https://files.slack.com/download/report.pdf",
	}

	got, err := FormatJSON(FileToJSON(file))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "url_private") || strings.Contains(got, "files.slack.com") {
		t.Fatalf("public attachment JSON leaked a private URL:\n%s", got)
	}
	if !strings.Contains(got, `"download_command": "slk download F12345678"`) {
		t.Fatalf("public attachment JSON omitted the download command:\n%s", got)
	}
}

func TestMessagesToJSONSetsIdentityAndDownloadCommand(t *testing.T) {
	messages := []api.Message{
		{
			User: testSelfID,
			Text: "mine",
			Ts:   "1700000000.000000",
			Files: []api.File{
				{ID: "F12345678", Name: "report.pdf"},
			},
		},
		{User: testOtherID, Text: "theirs", Ts: "1700000001.000000"},
	}

	got := MessagesToJSON(messages, testResolveUser, testSelfID)
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if !got[0].IsSelf {
		t.Fatal("authenticated user's message has is_self=false")
	}
	if got[1].IsSelf {
		t.Fatal("other user's message has is_self=true")
	}
	if got[0].Files[0].DownloadCommand != "slk download F12345678" {
		t.Fatalf("download_command = %q, want runnable file-ID command", got[0].Files[0].DownloadCommand)
	}
}

func TestFormatSearchResultsMarksAuthenticatedUser(t *testing.T) {
	const permalink = "https://example.slack.com/archives/C12345678/p1700000000000000?thread_ts=1700000000.000000&cid=C12345678"
	result := &api.SearchResult{}
	result.Messages.Total = 2
	result.Messages.Matches = []api.SearchMatch{
		{
			User:      testSelfID,
			Text:      "mine",
			Ts:        "1700000000.000000",
			Channel:   api.SearchChannel{Name: "general"},
			Permalink: permalink,
			Files: []api.File{
				{ID: "F12345678", Name: "report.pdf", Size: 1024, Mimetype: "application/pdf"},
			},
		},
		{User: testOtherID, Text: "theirs", Ts: "1700000001.000000", Channel: api.SearchChannel{Name: "general"}},
	}

	got := FormatSearchResults(result, testResolveUser, testSelfID)
	if !strings.Contains(got, "@owner (me): mine") {
		t.Fatalf("authenticated search author was not marked as me:\n%s", got)
	}
	if strings.Contains(got, "@teammate (me)") {
		t.Fatalf("other search author was incorrectly marked as me:\n%s", got)
	}
	if !strings.Contains(got, "[pdf] report.pdf (1.0KB) — slk download F12345678") {
		t.Fatalf("search attachment did not include a runnable download command:\n%s", got)
	}
	if !strings.Contains(got, "[open context — slk open '"+permalink+"']") {
		t.Fatalf("search result did not include a safely quoted context command:\n%s", got)
	}
}

func TestSearchMatchesToJSONSetsIdentityAndDownloadCommand(t *testing.T) {
	const permalink = "https://example.slack.com/archives/C12345678/p1700000000000000"
	matches := []api.SearchMatch{
		{
			User:      testSelfID,
			Text:      "mine",
			Permalink: permalink,
			Files: []api.File{
				{ID: "F12345678", Name: "report.pdf"},
			},
		},
		{User: testOtherID, Text: "theirs"},
	}

	got := SearchMatchesToJSON(matches, testSelfID)
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2", len(got))
	}
	if !got[0].IsSelf {
		t.Fatal("authenticated user's search match has is_self=false")
	}
	if got[1].IsSelf {
		t.Fatal("other user's search match has is_self=true")
	}
	if got[0].Files[0].DownloadCommand != "slk download F12345678" {
		t.Fatalf("download_command = %q, want runnable file-ID command", got[0].Files[0].DownloadCommand)
	}
	if got[0].OpenCommand != "slk open '"+permalink+"'" {
		t.Fatalf("open_command = %q, want runnable permalink command", got[0].OpenCommand)
	}
	if got[1].OpenCommand != "" {
		t.Fatalf("missing permalink produced open_command %q", got[1].OpenCommand)
	}
}
