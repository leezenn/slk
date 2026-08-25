package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leezenn/slk/internal/api"
)

type fakeActivitySearcher struct {
	results map[string]*api.SearchResult
	errors  map[string]error
	queries []string
	limits  []int
}

func (f *fakeActivitySearcher) SearchMessages(query string, limit int) (*api.SearchResult, error) {
	f.queries = append(f.queries, query)
	f.limits = append(f.limits, limit)
	if err := f.errors[query]; err != nil {
		return nil, err
	}
	return f.results[query], nil
}

func activityResult(matches ...api.SearchMatch) *api.SearchResult {
	result := &api.SearchResult{}
	result.Messages.Matches = matches
	return result
}

type fakeActivityUserResolver struct {
	infoIDs   []string
	nameQuery string
}

func (f *fakeActivityUserResolver) GetUserInfo(userID string) (*api.User, error) {
	f.infoIDs = append(f.infoIDs, userID)
	return &api.User{ID: userID, Name: "resolved"}, nil
}

func (f *fakeActivityUserResolver) FindUserByName(query string) (*api.User, error) {
	f.nameQuery = query
	return &api.User{ID: "U87654321", Name: query}, nil
}

func TestResolveActivityPersonDefaultsToMeAndAcceptsPeople(t *testing.T) {
	const selfID = "U12345678"
	tests := []struct {
		name       string
		person     string
		wantID     string
		wantLookup string
	}{
		{name: "default me", wantID: selfID},
		{name: "explicit me", person: "@me", wantID: selfID},
		{name: "handle", person: " @alex ", wantID: "U87654321", wantLookup: "alex"},
		{name: "user ID", person: "U99999999", wantID: "U99999999"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeActivityUserResolver{}
			user, err := resolveActivityPerson(client, selfID, test.person)
			if err != nil {
				t.Fatalf("resolveActivityPerson returned error: %v", err)
			}
			if user.ID != test.wantID || client.nameQuery != test.wantLookup {
				t.Fatalf("user = %#v name lookup = %q", user, client.nameQuery)
			}
		})
	}
}

func TestCollectActivityBuildsPersonCenteredField(t *testing.T) {
	const targetID = "U12345678"
	mention := "<@" + targetID + ">"
	authoredQuery := "from:" + mention
	client := &fakeActivitySearcher{results: map[string]*api.SearchResult{
		authoredQuery: activityResult(
			api.SearchMatch{User: targetID, Text: "new authored", Ts: "1700000300.000001", Channel: api.SearchChannel{ID: "C1", Name: "deployments"}},
			api.SearchMatch{User: targetID, Text: "overlap " + mention, Ts: "1700000200.000001", Channel: api.SearchChannel{ID: "C1", Name: "deployments"}},
			api.SearchMatch{User: "U99999999", Text: "search false positive", Ts: "1700000500.000001", Channel: api.SearchChannel{ID: "C3", Name: "random"}},
			api.SearchMatch{User: targetID, Text: "too old", Ts: "1699999999.000001", Channel: api.SearchChannel{ID: "C1", Name: "deployments"}},
		),
		mention: activityResult(
			api.SearchMatch{User: "U87654321", Text: "please ask " + mention, Ts: "1700000400.000001", Channel: api.SearchChannel{ID: "C2", Name: "product"}},
			api.SearchMatch{User: targetID, Text: "overlap " + mention, Ts: "1700000200.000001", Channel: api.SearchChannel{ID: "C1", Name: "deployments"}},
			api.SearchMatch{User: "U87654321", Text: "no canonical mention", Ts: "1700000600.000001", Channel: api.SearchChannel{ID: "C4", Name: "noise"}},
		),
	}}

	items, err := collectActivity(client, targetID, time.Unix(1_700_000_000, 0), 10)
	if err != nil {
		t.Fatalf("collectActivity returned error: %v", err)
	}
	if got := strings.Join(client.queries, ","); got != authoredQuery+","+mention {
		t.Fatalf("queries = %q, want exact authored and mention searches", got)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %#v", len(items), items)
	}
	if items[0].Match.Channel.ID != "C2" || items[1].Match.Ts != "1700000300.000001" || items[2].Match.Ts != "1700000200.000001" {
		t.Fatalf("items are not newest-first: %#v", items)
	}
	if got := activityReasonLabel(items[2].Reasons); got != "authored, mentioned" {
		t.Fatalf("deduped reasons = %q", got)
	}
	groups := groupActivity(items)
	if len(groups) != 2 || groups[0].Channel.ID != "C2" || len(groups[1].Items) != 2 {
		t.Fatalf("conversation groups = %#v", groups)
	}
}

func TestCollectActivityPreservesSignalDiversityAtTheLimit(t *testing.T) {
	const targetID = "U12345678"
	mention := "<@" + targetID + ">"
	client := &fakeActivitySearcher{results: map[string]*api.SearchResult{
		"from:" + mention: activityResult(
			api.SearchMatch{User: targetID, Text: "authored one", Ts: "1700000500.000001", Channel: api.SearchChannel{ID: "C1"}},
			api.SearchMatch{User: targetID, Text: "authored two", Ts: "1700000400.000001", Channel: api.SearchChannel{ID: "C1"}},
			api.SearchMatch{User: targetID, Text: "authored three", Ts: "1700000300.000001", Channel: api.SearchChannel{ID: "C1"}},
		),
		mention: activityResult(
			api.SearchMatch{User: "U87654321", Text: "please ask " + mention, Ts: "1700000200.000001", Channel: api.SearchChannel{ID: "C2"}},
		),
	}}

	items, err := collectActivity(client, targetID, time.Unix(1_700_000_000, 0), 3)
	if err != nil {
		t.Fatalf("collectActivity returned error: %v", err)
	}
	if len(items) != 3 || !activityHasReason(items[2].Reasons, activityMentioned) {
		t.Fatalf("limited items buried the mention: %#v", items)
	}
	if items[0].Match.Ts != "1700000500.000001" || items[1].Match.Ts != "1700000400.000001" {
		t.Fatalf("remaining slots did not preserve exact recency: %#v", items)
	}
}

func TestCollectActivityStopsWhenEitherSearchFails(t *testing.T) {
	const targetID = "U12345678"
	mention := "<@" + targetID + ">"
	client := &fakeActivitySearcher{
		results: map[string]*api.SearchResult{"from:" + mention: activityResult()},
		errors:  map[string]error{mention: errors.New("search unavailable")},
	}

	_, err := collectActivity(client, targetID, time.Time{}, 20)
	if err == nil || !strings.Contains(err.Error(), "searching mentioned activity") {
		t.Fatalf("error = %v, want mention-search context", err)
	}
}

func TestFormatActivityExplainsFieldAndContinuations(t *testing.T) {
	const permalink = "https://example.slack.com/archives/C12345678/p1700000400000001"
	target := &api.User{ID: "U12345678", Name: "alex"}
	groups := []activityGroup{{
		Channel: api.SearchChannel{ID: "C12345678", Name: "deployments"},
		Items: []activityItem{{
			Match: api.SearchMatch{
				User: target.ID, Text: "please ask <@U87654321>", Ts: "1700000400.000001",
				Channel: api.SearchChannel{ID: "C12345678", Name: "deployments"}, Permalink: permalink,
			},
			Reasons: []activityReason{activityAuthored},
		}},
	}}
	resolveUser := func(userID string) string {
		if userID == target.ID {
			return "alex"
		}
		return "sam"
	}

	got := formatActivity(target, target.ID, time.Unix(1_700_000_000, 0), time.Unix(1_700_004_600, 0), groups, resolveUser)
	for _, want := range []string{
		"Activity around @alex (me)",
		"Search-derived; limited to messages visible to the authenticated Slack user.",
		"1 matching message across 1 conversation.",
		"#deployments — latest 1h ago",
		"authored · 1h ago · @alex (me)",
		"please ask @sam",
		"Open: slk open '" + permalink + "'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("activity output omitted %q:\n%s", want, got)
		}
	}
}

func TestActivityJSONCarriesIdentityReasonsAndTimestamps(t *testing.T) {
	target := &api.User{ID: "U12345678", Name: "alex", Profile: api.UserProfile{DisplayName: "Alex"}}
	groups := []activityGroup{{
		Channel: api.SearchChannel{ID: "C12345678", Name: "product"},
		Items: []activityItem{{
			Match:   api.SearchMatch{User: target.ID, Text: "decision", Ts: "1700000400.000001", Channel: api.SearchChannel{ID: "C12345678", Name: "product"}},
			Reasons: []activityReason{activityAuthored},
		}},
	}}

	got := activityJSON(target, target.ID, time.Unix(1_700_000_000, 0), groups)
	if !got.OK || !got.SearchDerived || !got.Person.IsSelf || got.Person.DisplayName != "Alex" {
		t.Fatalf("identity payload = %#v", got)
	}
	if len(got.Conversations) != 1 || got.Conversations[0].Items[0].Message.Timestamp == "" {
		t.Fatalf("conversation payload omitted temporal data: %#v", got.Conversations)
	}
	if got.Conversations[0].Items[0].Reasons[0] != activityAuthored {
		t.Fatalf("reasons = %#v", got.Conversations[0].Items[0].Reasons)
	}
}
