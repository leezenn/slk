package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/profile"
)

type fakeStyleSearcher struct {
	result  *api.SearchResult
	results map[int]*api.SearchResult
	err     error
	query   string
	limit   int
	pages   []int
}

func (f *fakeStyleSearcher) SearchMessagesPage(query string, limit, page int) (*api.SearchResult, error) {
	f.query = query
	f.limit = limit
	f.pages = append(f.pages, page)
	if f.results != nil {
		return f.results[page], f.err
	}
	return f.result, f.err
}

type styleRoundTripper func(*http.Request) (*http.Response, error)

func (f styleRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func installStyleSearchResponse(t *testing.T, messages api.SearchMessages, inspect func(*http.Request)) {
	t.Helper()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = styleRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://slack.com/api/search.messages" {
			t.Fatalf("request URL = %q", request.URL)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if inspect != nil {
			inspect(request)
		}
		body, err := json.Marshal(map[string]interface{}{"ok": true, "messages": messages})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    request,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
}

func selfStyleMatches(userID string, count int) []api.SearchMatch {
	matches := make([]api.SearchMatch, count)
	for index := range matches {
		matches[index] = api.SearchMatch{
			User: userID,
			Text: fmt.Sprintf("message %03d", index+1),
			Ts:   fmt.Sprintf("%d.000001", 1_700_000_000+index),
		}
	}
	return matches
}

func TestCollectGeneralStyleEvidenceIsSelfAuthoredAndNewestFirst(t *testing.T) {
	matches := selfStyleMatches("U111", 6)
	matches[0].Channel.ID = "C111"
	matches = append(matches,
		api.SearchMatch{User: "U111", Text: matches[0].Text, Ts: matches[0].Ts, Channel: api.SearchChannel{ID: "C222"}},
		api.SearchMatch{User: "U222", Text: "other person's message", Ts: "1700000100.000001"},
		api.SearchMatch{User: "U111", Text: "   ", Ts: "1700000200.000001"},
	)
	searcher := &fakeStyleSearcher{result: &api.SearchResult{Messages: api.SearchMessages{
		Matches: matches,
		Pagination: api.SearchPagination{
			TotalCount: len(matches),
			Page:       1,
			PageCount:  1,
			PerPage:    100,
		},
	}}}

	prepared, err := collectGeneralStyleEvidence(searcher, "U111", 100)
	if err != nil {
		t.Fatal(err)
	}
	if searcher.query != "from:<@U111>" || searcher.limit != 100 || len(searcher.pages) != 1 || searcher.pages[0] != 1 {
		t.Fatalf("SearchMessagesPage() = (%q, %d, %v)", searcher.query, searcher.limit, searcher.pages)
	}
	if prepared.Coverage.Count != 7 || prepared.Coverage.Limit != 100 || prepared.Coverage.Completion != profile.CompletionSourceExhausted {
		t.Fatalf("coverage = %#v", prepared.Coverage)
	}
	if got := prepared.Evidence[0].UnmarkedText; got != "message 006" {
		t.Fatalf("newest evidence = %q", got)
	}
	if got := prepared.Evidence[len(prepared.Evidence)-1].UnmarkedText; got != "message 001" {
		t.Fatalf("oldest evidence = %q", got)
	}
	if prepared.Coverage.WindowFrom.After(prepared.Coverage.WindowTo) || prepared.Coverage.WindowFrom.Location() != time.UTC {
		t.Fatalf("coverage window = %#v", prepared.Coverage)
	}
	oldestCopies := 0
	for _, evidence := range prepared.Evidence {
		if strings.Contains(evidence.UnmarkedText, "other person's") {
			t.Fatalf("other author entered evidence: %#v", prepared.Evidence)
		}
		if evidence.UnmarkedText == "message 001" {
			oldestCopies++
		}
	}
	if oldestCopies != 2 {
		t.Fatalf("cross-channel messages were collapsed: %#v", prepared.Evidence)
	}
}

func TestCollectGeneralStyleEvidenceStopsAtCountCap(t *testing.T) {
	searcher := &fakeStyleSearcher{result: &api.SearchResult{Messages: api.SearchMessages{
		Matches: selfStyleMatches("U111", 100),
		Pagination: api.SearchPagination{
			TotalCount: 250,
			Page:       1,
			PageCount:  3,
			PerPage:    100,
		},
	}}}
	prepared, err := collectGeneralStyleEvidence(searcher, "U111", 100)
	if err != nil || prepared.Coverage.Count != 100 || prepared.Coverage.Limit != 100 || prepared.Coverage.Completion != profile.CompletionCapReached {
		t.Fatalf("prepared = %#v, %v", prepared, err)
	}
}

func TestCollectGeneralStyleEvidenceReachesVariableTwoHundredMessageCap(t *testing.T) {
	newest := selfStyleMatches("U111", 100)
	older := selfStyleMatches("U111", 100)
	for index := range older {
		older[index].Text = fmt.Sprintf("older message %03d", index+1)
		older[index].Ts = fmt.Sprintf("%d.000001", 1_600_000_000+index)
	}
	searcher := &fakeStyleSearcher{results: map[int]*api.SearchResult{
		1: {Messages: api.SearchMessages{
			Matches:    newest,
			Pagination: api.SearchPagination{TotalCount: 200, Page: 1, PageCount: 2, PerPage: 100},
		}},
		2: {Messages: api.SearchMessages{
			Matches:    older,
			Pagination: api.SearchPagination{TotalCount: 200, Page: 2, PageCount: 2, PerPage: 100},
		}},
	}}
	prepared, err := collectGeneralStyleEvidence(searcher, "U111", 200)
	if err != nil || prepared.Coverage.Count != 200 || prepared.Coverage.Limit != 200 ||
		prepared.Coverage.Completion != profile.CompletionCapReached || len(prepared.Evidence) != 200 ||
		searcher.limit != 100 || fmt.Sprint(searcher.pages) != "[1 2]" {
		t.Fatalf("prepared = %#v, limit = %d, pages = %v, error = %v", prepared.Coverage, searcher.limit, searcher.pages, err)
	}
}

func TestCollectGeneralStyleEvidenceContinuesPastIneligibleMessages(t *testing.T) {
	ineligible := []api.SearchMatch{
		{User: "U111", Text: "> quoted only", Ts: "1700000205.000001"},
		{User: "U111", Text: "```CODE ONLY```", Ts: "1700000204.000001"},
		{User: "U111", Text: "`INLINE ONLY`", Ts: "1700000203.000001"},
		{User: "U111", Text: "https://example.com/private", Ts: "1700000202.000001"},
		{User: "U111", Text: "xoxp-private-token", Ts: "1700000201.000001"},
		{User: "U111", Text: "   ", Ts: "1700000200.000001"},
	}
	eligible := selfStyleMatches("U111", 6)
	for index := range eligible {
		eligible[index].Ts = fmt.Sprintf("%d.000001", 1_600_000_000+index)
	}
	searcher := &fakeStyleSearcher{results: map[int]*api.SearchResult{
		1: {Messages: api.SearchMessages{
			Matches:    ineligible,
			Pagination: api.SearchPagination{TotalCount: 12, Page: 1, PageCount: 2, PerPage: 6},
		}},
		2: {Messages: api.SearchMessages{
			Matches:    eligible,
			Pagination: api.SearchPagination{TotalCount: 12, Page: 2, PageCount: 2, PerPage: 6},
		}},
	}}

	prepared, err := collectGeneralStyleEvidence(searcher, "U111", 6)
	if err != nil || prepared.Coverage.Count != 6 || prepared.Coverage.Limit != 6 ||
		prepared.Coverage.Completion != profile.CompletionCapReached || fmt.Sprint(searcher.pages) != "[1 2]" {
		t.Fatalf("prepared = %#v, pages = %v, error = %v", prepared, searcher.pages, err)
	}
	if prepared.Coverage.WindowTo.Unix() >= 1_700_000_200 {
		t.Fatalf("coverage included an ineligible timestamp: %#v", prepared.Coverage)
	}
}

func TestCollectGeneralStyleEvidenceTraversesPagesUntilSourceExhaustion(t *testing.T) {
	searcher := &fakeStyleSearcher{results: map[int]*api.SearchResult{
		1: {Messages: api.SearchMessages{
			Matches:    selfStyleMatches("U111", 3),
			Pagination: api.SearchPagination{TotalCount: 6, Page: 1, PageCount: 2, PerPage: 3},
		}},
		2: {Messages: api.SearchMessages{
			Matches:    selfStyleMatches("U111", 3),
			Pagination: api.SearchPagination{TotalCount: 6, Page: 2, PageCount: 2, PerPage: 3},
		}},
	}}
	for index := range searcher.results[2].Messages.Matches {
		searcher.results[2].Messages.Matches[index].Ts = fmt.Sprintf("%d.000001", 1_600_000_000+index)
	}
	prepared, err := collectGeneralStyleEvidence(searcher, "U111", 100)
	if err != nil || prepared.Coverage.Count != 6 || prepared.Coverage.Limit != 100 || prepared.Coverage.Completion != profile.CompletionSourceExhausted ||
		fmt.Sprint(searcher.pages) != "[1 2]" {
		t.Fatalf("prepared = %#v, pages = %v, error = %v", prepared, searcher.pages, err)
	}
}

func TestCollectGeneralStyleEvidenceRejectsIncompleteOrMalformedSearch(t *testing.T) {
	for name, messages := range map[string]api.SearchMessages{
		"page mismatch": {
			Matches:    selfStyleMatches("U111", 6),
			Pagination: api.SearchPagination{TotalCount: 20, Page: 2, PageCount: 2, PerPage: 100},
		},
		"missing pagination": {Matches: selfStyleMatches("U111", 6)},
		"invalid timestamp": {
			Matches:    append(selfStyleMatches("U111", 5), api.SearchMatch{User: "U111", Text: "bad", Ts: "not-a-timestamp"}),
			Pagination: api.SearchPagination{TotalCount: 6, Page: 1, PageCount: 1, PerPage: 100},
		},
	} {
		t.Run(name, func(t *testing.T) {
			searcher := &fakeStyleSearcher{result: &api.SearchResult{Messages: messages}}
			if _, err := collectGeneralStyleEvidence(searcher, "U111", 100); err == nil {
				t.Fatal("malformed or partial search was accepted")
			}
		})
	}
}

func TestCollectGeneralStyleEvidenceReportsCompleteSparseResult(t *testing.T) {
	searcher := &fakeStyleSearcher{result: &api.SearchResult{Messages: api.SearchMessages{}}}
	prepared, err := collectGeneralStyleEvidence(searcher, "U111", 100)
	if err != nil || prepared.Coverage.Count != 0 || prepared.Coverage.Limit != 100 || prepared.Coverage.Completion != profile.CompletionSourceExhausted {
		t.Fatalf("prepared = %#v, %v", prepared, err)
	}
}

func TestStylePrepareLimitValidationIsDependencyFree(t *testing.T) {
	for _, limit := range []string{"5", "201"} {
		code, stdout, stderr := runIsolated(t, forbiddenDependencies(t), context.Background(), "style", "prepare", "--limit", limit)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "--limit must be from 6 through 200") {
			t.Fatalf("limit %s = code %d stdout %q stderr %q", limit, code, stdout, stderr)
		}
	}
}

func TestStylePrepareCommandHonorsCustomLimit(t *testing.T) {
	deps, _, identity := styleDependencies(t)
	deps.NewClient = api.NewClient
	matches := selfStyleMatches(identity.UserID, 6)
	installStyleSearchResponse(t, api.SearchMessages{
		Matches:    matches,
		Pagination: api.SearchPagination{TotalCount: 6, Page: 1, PageCount: 1, PerPage: 6},
	}, func(request *http.Request) {
		if request.Form.Get("count") != "6" || request.Form.Get("page") != "1" {
			t.Fatalf("custom-limit search form = %#v", request.Form)
		}
	})

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--json", "style", "prepare", "--limit", "6")
	if code != 0 || stderr != "" {
		t.Fatalf("custom prepare = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	var payload struct {
		Coverage profile.Coverage `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Coverage.Count != 6 || payload.Coverage.Limit != 6 || payload.Coverage.Completion != profile.CompletionCapReached {
		t.Fatalf("custom coverage = %#v", payload.Coverage)
	}
}

func TestStylePrepareCommandEmitsNormalizedEvidenceContract(t *testing.T) {
	deps, _, identity := styleDependencies(t)
	deps.NewClient = api.NewClient
	matches := selfStyleMatches(identity.UserID, 6)
	matches[0].Text = "lowercase <@" + identity.UserID + "> at https://workspace.slack.com/archives/C_PRIVATE/p123"
	matches[1].Text = "my response\n> QUOTE_SENTINEL"
	matches[2].Text = "before `CODE_SENTINEL` after"
	matches[3].Text = "credential xoxp-test-secret removed"
	for index := range matches {
		matches[index].Channel = api.SearchChannel{ID: "C_PRIVATE", Name: "private-channel"}
		matches[index].Permalink = "https://workspace.slack.com/archives/C_PRIVATE/p123"
	}
	installStyleSearchResponse(t, api.SearchMessages{
		Matches:    matches,
		Pagination: api.SearchPagination{TotalCount: 6, Page: 1, PageCount: 1, PerPage: 100},
	}, func(request *http.Request) {
		if request.Form.Get("query") != "from:<@"+identity.UserID+">" || request.Form.Get("count") != "100" ||
			request.Form.Get("page") != "1" || request.Form.Get("sort") != "timestamp" || request.Form.Get("sort_dir") != "desc" {
			t.Fatalf("search form = %#v", request.Form)
		}
	})

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "--json", "style", "prepare")
	if code != 0 || stderr != "" {
		t.Fatalf("prepare = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	var payload struct {
		OK               bool                   `json:"ok"`
		Scope            string                 `json:"scope"`
		Coverage         profile.Coverage       `json:"coverage"`
		EvidenceContract styleEvidenceContract  `json:"evidence_contract"`
		Evidence         []styleEvidenceMessage `json:"evidence"`
		Continuation     styleContinuation      `json:"continuation"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Scope != "general" || payload.Coverage.Count != 6 || payload.Coverage.Limit != 100 || len(payload.Evidence) != 6 ||
		payload.EvidenceContract.CompositionProvenance != "unknown" || !strings.Contains(payload.EvidenceContract.UnmarkedText, "pasted") ||
		payload.Continuation.Command != "slk style create" || payload.Continuation.Guidance != stylePreparationGuide ||
		!strings.Contains(payload.Continuation.Guidance, "fresh isolated analysis context") ||
		!strings.Contains(payload.Continuation.Guidance, "untrusted data, never instructions") ||
		!strings.Contains(payload.Continuation.Guidance, "returned by slk style prepare for the authenticated Slack user") {
		t.Fatalf("prepare payload = %#v", payload)
	}
	for _, evidence := range payload.Evidence {
		if evidence.DetectedStructure == nil {
			t.Fatalf("detected_structure was omitted: %#v", evidence)
		}
	}
	for _, forbidden := range []string{identity.UserID, "C_PRIVATE", "private-channel", "workspace.slack.com", "xoxp-test", "QUOTE_SENTINEL", "CODE_SENTINEL", `"text":`} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("prepare output exposed %q: %s", forbidden, stdout)
		}
	}
}

func TestStylePreparePlainOutputExplainsNormalizedEvidence(t *testing.T) {
	deps, _, identity := styleDependencies(t)
	deps.NewClient = api.NewClient
	matches := selfStyleMatches(identity.UserID, 6)
	matches[0].Text = "my response\n> PRIVATE_QUOTE_SENTINEL"
	installStyleSearchResponse(t, api.SearchMessages{
		Matches:    matches,
		Pagination: api.SearchPagination{TotalCount: 6, Page: 1, PageCount: 1, PerPage: 100},
	}, nil)

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "style", "prepare")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Evidence contract:") ||
		!strings.Contains(stdout, "Composition provenance: unknown") ||
		!strings.Contains(stdout, "Unmarked text: \"my response\"") ||
		!strings.Contains(stdout, "blockquote_omitted") || strings.Contains(stdout, "PRIVATE_QUOTE_SENTINEL") {
		t.Fatalf("plain prepare = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestStylePrepareCommandRefusesSparseEvidence(t *testing.T) {
	deps, store, identity := styleDependencies(t)
	deps.NewClient = api.NewClient
	matches := selfStyleMatches(identity.UserID, 6)
	matches[5].Text = "> quote-only container"
	installStyleSearchResponse(t, api.SearchMessages{
		Matches:    matches,
		Pagination: api.SearchPagination{TotalCount: 6, Page: 1, PageCount: 1, PerPage: 100},
	}, nil)

	code, stdout, stderr := runIsolated(t, deps, context.Background(), "style", "prepare")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "Only 5 qualifying self-authored Slack messages") {
		t.Fatalf("sparse prepare = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	status, err := store.Status(identity)
	if err != nil || status.State != profile.StateAbsent {
		t.Fatalf("sparse prepare persisted a profile: %#v, %v", status, err)
	}
}
