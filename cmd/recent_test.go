package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/leezenn/slk/internal/api"
)

func TestRecentSearchQueryStartsOneDayBeforeExactCutoff(t *testing.T) {
	zone := time.FixedZone("EEST", 3*60*60)
	cutoff := time.Date(2026, 8, 24, 22, 30, 0, 0, zone)
	if got := recentSearchQuery(cutoff); got != "after:2026-08-23" {
		t.Fatalf("recentSearchQuery() = %q", got)
	}
}

func TestCollectRecentOrdersAndDeduplicatesConversations(t *testing.T) {
	result := &api.SearchResult{}
	result.Messages.Total = 12
	result.Messages.Matches = []api.SearchMatch{
		{Type: "message", Ts: "1700000300.000001", Channel: api.SearchChannel{ID: "C1", Name: "deployments"}, Text: "older in channel"},
		{Type: "im", Ts: "1700000500.000001", Channel: api.SearchChannel{ID: "D1", Name: "U87654321"}, Text: "newest DM"},
		{Type: "message", Ts: "1700000450.000001", Channel: api.SearchChannel{ID: "C2", Name: "product"}, Text: "new channel"},
		{Type: "message", Ts: "1700000400.000001", Channel: api.SearchChannel{ID: "C1", Name: "deployments"}, Text: "newer in channel"},
		{Type: "message", Ts: "1699999999.000001", Channel: api.SearchChannel{ID: "C3", Name: "old"}, Text: "too old"},
	}
	cutoff := time.Unix(1_700_000_000, 0)

	all := collectRecent(result, cutoff, recentAll, 10)
	if all.QueryTotalHits != 12 || all.ScannedHits != 5 || len(all.Conversations) != 3 {
		t.Fatalf("all snapshot = %#v", all)
	}
	if all.Conversations[0].Kind != recentDM || all.Conversations[0].Latest.Channel.ID != "D1" {
		t.Fatalf("first conversation = %#v, want newest DM", all.Conversations[0])
	}
	if all.Conversations[2].Latest.Text != "newer in channel" {
		t.Fatalf("dedupe kept wrong C1 hit: %#v", all.Conversations[2])
	}

	dms := collectRecent(result, cutoff, recentDM, 10)
	if len(dms.Conversations) != 1 || dms.Conversations[0].Latest.Channel.ID != "D1" {
		t.Fatalf("DM snapshot = %#v", dms)
	}
	channels := collectRecent(result, cutoff, recentChannel, 1)
	if len(channels.Conversations) != 1 || channels.Conversations[0].Latest.Channel.ID != "C2" {
		t.Fatalf("limited channel snapshot = %#v", channels)
	}
}

func TestClassifyRecentConversationUsesDocumentedIMSignals(t *testing.T) {
	if got := classifyRecentConversation(api.SearchMatch{Type: "im", Channel: api.SearchChannel{ID: "C1"}}); got != recentDM {
		t.Fatalf("IM type classified as %q", got)
	}
	if got := classifyRecentConversation(api.SearchMatch{Type: "message", Channel: api.SearchChannel{ID: "D12345678"}}); got != recentDM {
		t.Fatalf("DM channel ID classified as %q", got)
	}
	if got := classifyRecentConversation(api.SearchMatch{Type: "group", Channel: api.SearchChannel{ID: "G12345678"}}); got != recentChannel {
		t.Fatalf("group classified as %q", got)
	}
}

func TestFormatRecentProvidesTemporalAndNavigationSignals(t *testing.T) {
	const permalink = "https://example.slack.com/archives/C12345678/p1700000400000001"
	cutoff := time.Unix(1_700_000_000, 0)
	snapshot := recentSnapshot{
		QueryTotalHits: 9, ScannedHits: 2,
		Conversations: []recentConversation{{
			Kind: recentChannel,
			Latest: api.SearchMatch{
				Type: "message", User: "U12345678", Text: "rollout paused", Ts: "1700000400.000001",
				Channel: api.SearchChannel{ID: "C12345678", Name: "deployments"}, Permalink: permalink,
			},
		}},
	}
	resolveUser := func(string) string { return "owner" }

	got := formatRecent(snapshot, "U12345678", cutoff, time.Unix(1_700_004_600, 0), recentAll, resolveUser)
	for _, want := range []string{
		"Recent conversations",
		"Search-derived from 2 scanned hits (9 matched the broad date query)",
		"ordered by each conversation's newest searchable message",
		"1 conversation.",
		"#deployments — 1h ago",
		"@owner (me): rollout paused",
		"Open: slk open '" + permalink + "'",
		"Read: slk read C12345678 --after 2023-11-14T22:13:20Z",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("recent output omitted %q:\n%s", want, got)
		}
	}
}

func TestFormatRecentExplainsFilteredEmptyScan(t *testing.T) {
	got := formatRecent(recentSnapshot{QueryTotalHits: 20, ScannedHits: 20}, "", time.Unix(1_700_000_000, 0), time.Unix(1_700_000_100, 0), recentDM, func(id string) string { return id })
	if !strings.Contains(got, "No dm conversations appeared in the 20 scanned hits.") {
		t.Fatalf("filtered empty output = %q", got)
	}
}

func TestRecentJSONNamesLatestSearchHitHonestly(t *testing.T) {
	cutoff := time.Unix(1_700_000_000, 0)
	snapshot := recentSnapshot{
		QueryTotalHits: 5, ScannedHits: 3,
		Conversations: []recentConversation{{
			Kind: recentDM,
			Latest: api.SearchMatch{
				Type: "im", User: "U87654321", Text: "hello", Ts: "1700000400.000001",
				Channel: api.SearchChannel{ID: "D12345678", Name: "U87654321"},
			},
		}},
	}

	got := recentJSON(snapshot, "U12345678", cutoff, recentAll)
	if !got.OK || !got.SearchDerived || got.QueryTotalHits != 5 || got.ScannedHits != 3 {
		t.Fatalf("recent payload = %#v", got)
	}
	conversation := got.Conversations[0]
	if conversation.Kind != recentDM || conversation.LatestSearchHit.Timestamp == "" {
		t.Fatalf("conversation payload = %#v", conversation)
	}
	if conversation.ReadCommand != "slk read D12345678 --after 2023-11-14T22:13:20Z" {
		t.Fatalf("read command = %q", conversation.ReadCommand)
	}
}
