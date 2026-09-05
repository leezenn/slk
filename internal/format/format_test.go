package format

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/presentation"
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

func TestFormatRelativeTime(t *testing.T) {
	now := time.Unix(1_700_086_400, 0)
	tests := []struct {
		name string
		ts   string
		want string
	}{
		{name: "just now", ts: "1700086370.000000", want: "just now"},
		{name: "minutes", ts: "1700085500.000000", want: "15m ago"},
		{name: "hours", ts: "1700075600.000000", want: "3h ago"},
		{name: "days", ts: "1699913600.000000", want: "2d ago"},
		{name: "invalid", ts: "not-a-timestamp", want: "unknown time"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := FormatRelativeTime(test.ts, now); got != test.want {
				t.Fatalf("FormatRelativeTime(%q) = %q, want %q", test.ts, got, test.want)
			}
		})
	}
}

func TestSearchChannelLabel(t *testing.T) {
	if got := SearchChannelLabel(api.SearchChannel{ID: "C12345678", Name: "general"}, testResolveUser); got != "#general" {
		t.Fatalf("channel label = %q", got)
	}
	if got := SearchChannelLabel(api.SearchChannel{ID: "D12345678", Name: testOtherID}, testResolveUser); got != "@teammate" {
		t.Fatalf("DM label = %q", got)
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
	if !strings.Contains(got, "@owner (me): │ mine") {
		t.Fatalf("authenticated author was not marked as me:\n%s", got)
	}
	if !strings.Contains(got, "@teammate: │ theirs") {
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

func TestMessagePresentationNormalizationInPlainAndJSONOutput(t *testing.T) {
	contextBlock := formatRawBlock(t, map[string]interface{}{
		"type":     "context",
		"elements": []interface{}{map[string]string{"type": "mrkdwn", "text": "prefix"}},
	})
	section := func(expand interface{}) json.RawMessage {
		block := map[string]interface{}{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": "body"},
		}
		if expand != nil {
			block["expand"] = expand
		}
		return formatRawBlock(t, block)
	}
	tests := []struct {
		name   string
		blocks []json.RawMessage
		want   presentation.Mode
	}{
		{name: "expanded", blocks: []json.RawMessage{contextBlock, section(true)}, want: presentation.AlwaysExpanded},
		{name: "managed omitted", blocks: []json.RawMessage{contextBlock, section(nil)}, want: presentation.SlackManaged},
		{name: "managed false", blocks: []json.RawMessage{contextBlock, section(false)}, want: presentation.SlackManaged},
		{name: "native rich text", blocks: []json.RawMessage{formatRawBlock(t, map[string]interface{}{"type": "rich_text", "elements": []interface{}{}})}, want: presentation.SlackManaged},
		{name: "mixed omitted", blocks: []json.RawMessage{contextBlock, section(true), section(false)}},
		{name: "custom omitted", blocks: []json.RawMessage{formatRawBlock(t, map[string]interface{}{"type": "actions", "elements": []interface{}{}})}},
		{name: "absent omitted"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			messages := []api.Message{{User: testOtherID, Text: "body", Ts: "1700000001.000000", Blocks: test.blocks}}
			plain := FormatMessages(messages, "general", testResolveUser, testSelfID)
			jsonMessages := MessagesToJSON(messages, testResolveUser, testSelfID)
			if len(jsonMessages) != 1 {
				t.Fatalf("MessagesToJSON() length = %d", len(jsonMessages))
			}
			if test.want == "" {
				if strings.Contains(plain, "Presentation:") || jsonMessages[0].MessagePresentation != "" {
					t.Fatalf("unknown presentation was guessed: plain %q JSON %#v", plain, jsonMessages[0])
				}
				return
			}
			if !strings.Contains(plain, "Presentation: "+string(test.want)) || jsonMessages[0].MessagePresentation != test.want {
				t.Fatalf("presentation output = plain %q JSON %#v, want %q", plain, jsonMessages[0], test.want)
			}
		})
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
	if !strings.Contains(got, "@owner (me): │ mine") {
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
			Ts:        "1700000000.000000",
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
	if got[0].Timestamp != "2023-11-14T22:13:20Z" {
		t.Fatalf("timestamp = %q, want RFC3339 UTC", got[0].Timestamp)
	}
	if got[1].OpenCommand != "" {
		t.Fatalf("missing permalink produced open_command %q", got[1].OpenCommand)
	}
	encoded, err := FormatJSON(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "message_presentation") {
		t.Fatalf("search-derived JSON guessed presentation: %s", encoded)
	}
}

func TestResolveTextCoversReadableSlackReferenceForms(t *testing.T) {
	got := ResolveText(
		"<@W12345678> <#G12345678> <mailto:one@example.test|email> <!here> <!subteam^S12345678|@ops> <!date^0^{date}|epoch>",
		func(id string) string { return "resolved-" + id },
	)
	want := "@resolved-W12345678 #G12345678 email (mailto:one@example.test) @here @ops epoch"
	if got != want {
		t.Fatalf("ResolveText() = %q, want %q", got, want)
	}
}

func TestFormatMessagesLabelsTrustAndSemanticBoundaries(t *testing.T) {
	blockOnly := formatRawBlock(t, map[string]interface{}{
		"type": "rich_text",
		"elements": []interface{}{
			map[string]interface{}{"type": "rich_text_quote", "elements": []interface{}{map[string]string{"type": "text", "text": "quoted body"}}},
		},
	})
	messages := []api.Message{
		{User: testSelfID, Text: "first line\nsecond line", Ts: "1700000000.000000"},
		{BotID: "B12345678", Username: "helper", Ts: "1700000001.000000", ReplyCount: 1, Blocks: []json.RawMessage{blockOnly}},
	}

	got := FormatMessages(messages, "general", testResolveUser, testSelfID)
	for _, want := range []string{
		SlackContentNotice,
		"@owner (me): │ first line\n    │ second line",
		"[history fallback text; block structure unavailable]",
		"@helper [bot] [thread parent]:",
		"[quote] │ quoted body",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("semantic message output omitted %q:\n%s", want, got)
		}
	}
	if strings.Count(got, SlackContentNotice) != 1 {
		t.Fatalf("trust notice count = %d, want 1:\n%s", strings.Count(got, SlackContentNotice), got)
	}
}

func TestMessagesToJSONRetainsBlockOnlySemanticContent(t *testing.T) {
	block := formatRawBlock(t, map[string]interface{}{
		"type": "section",
		"text": map[string]string{"type": "plain_text", "text": "block-only body"},
	})
	messages := []api.Message{{BotID: "B12345678", Username: "helper", Ts: "1700000000.000000", ReplyCount: 2, Blocks: []json.RawMessage{block}}}
	got := MessagesToJSON(messages, testResolveUser, testSelfID)
	if len(got) != 1 {
		t.Fatalf("block-only messages = %d, want 1", len(got))
	}
	message := got[0]
	if message.Text != "" || message.AuthorKind != AuthorBot || message.ThreadRole != ThreadParent {
		t.Fatalf("semantic metadata = %#v", message)
	}
	if message.SemanticContent.Representation != HistoryBlocks || len(message.SemanticContent.Parts) != 1 || message.SemanticContent.Parts[0].Text != "block-only body" {
		t.Fatalf("semantic content = %#v", message.SemanticContent)
	}
	encoded, err := FormatJSON(message)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, `"blocks"`) || !strings.Contains(encoded, `"semantic_content"`) {
		t.Fatalf("JSON exposed raw blocks or omitted semantic content: %s", encoded)
	}
}

func TestFormatSearchResultsLabelsLimitedSemanticContentOnce(t *testing.T) {
	result := &api.SearchResult{}
	result.Messages.Total = 1
	result.Messages.Matches = []api.SearchMatch{{
		User: testOtherID, Text: "> quoted <@U12345678>", Ts: "1700000000.000000",
		Channel: api.SearchChannel{Name: "general"},
	}}
	got := FormatSearchResults(result, testResolveUser, testSelfID)
	for _, want := range []string{SlackContentNotice, SearchContentNotice, "[quote] │ quoted @owner"} {
		if !strings.Contains(got, want) {
			t.Fatalf("search output omitted %q:\n%s", want, got)
		}
	}
	if strings.Count(got, SearchContentNotice) != 1 {
		t.Fatalf("search limitation count = %d, want 1:\n%s", strings.Count(got, SearchContentNotice), got)
	}
}

func TestSearchMatchToJSONResolvedAddsSourceHonestSemantics(t *testing.T) {
	match := api.SearchMatch{User: testOtherID, Text: "hello <@U12345678>"}
	got := SearchMatchToJSONResolved(match, testResolveUser, testSelfID)
	if got.AuthorKind != AuthorSlackUser || got.ThreadRole != ThreadUnknown {
		t.Fatalf("search metadata = %#v", got)
	}
	content := got.SemanticContent
	if content.Representation != SearchTextOnly || content.CompositionProvenance != "unknown" || len(content.Parts) != 1 || content.Parts[0].Text != "hello @owner" {
		t.Fatalf("search semantic content = %#v", content)
	}
	if got.Text != match.Text {
		t.Fatalf("legacy search text changed from %q to %q", match.Text, got.Text)
	}
}

func TestSemanticMrkdwnPreservesLabelledDestinations(t *testing.T) {
	message := api.Message{Blocks: []json.RawMessage{formatRawBlock(t, map[string]interface{}{
		"type": "section",
		"text": map[string]string{
			"type": "mrkdwn",
			"text": "See <https://example.test/report|quarterly report> or <mailto:one@example.test|email>",
		},
	})}}
	got := MessagesToJSON([]api.Message{message}, testResolveUser, testSelfID)
	if len(got) != 1 || len(got[0].SemanticContent.Parts) != 1 {
		t.Fatalf("semantic link message = %#v", got)
	}
	want := "See quarterly report (https://example.test/report) or email (mailto:one@example.test)"
	if got[0].SemanticContent.Parts[0].Text != want {
		t.Fatalf("semantic links = %q, want %q", got[0].SemanticContent.Parts[0].Text, want)
	}

	fallback := ProjectMessageContent(api.Message{Text: "<https://example.test/report|quarterly report>"}, nil)
	if len(fallback.Parts) != 1 || fallback.Parts[0].Text != "quarterly report (https://example.test/report)" {
		t.Fatalf("fallback link semantics = %#v", fallback)
	}

	search := SearchMatchToJSONResolved(api.SearchMatch{Text: "<https://example.test|site>"}, nil, "")
	if len(search.SemanticContent.Parts) != 1 || search.SemanticContent.Parts[0].Text != "site (https://example.test)" {
		t.Fatalf("search link semantics = %#v", search.SemanticContent)
	}
}

func formatRawBlock(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
