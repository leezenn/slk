package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var activityUserIDRe = regexp.MustCompile(`^U[A-Z0-9]{8,}$`)

type activityOptions struct {
	since string
	limit int
}

type activityReason string

const (
	activityAuthored  activityReason = "authored"
	activityMentioned activityReason = "mentioned"
)

type activityItem struct {
	Match   api.SearchMatch
	Reasons []activityReason
}

type activityGroup struct {
	Channel api.SearchChannel
	Items   []activityItem
}

type activitySearcher interface {
	SearchMessages(query string, limit int) (*api.SearchResult, error)
}

type activityUserResolver interface {
	FindUserByName(query string) (*api.User, error)
	GetUserInfo(userID string) (*api.User, error)
}

type activityClient interface {
	selfIdentifier
	activitySearcher
	activityUserResolver
	BuildUserCache() error
	ResolveUser(userID string) string
}

func newActivityCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &activityOptions{since: "24h", limit: 20}
	command := &cobra.Command{
		Use:   "activity [person]",
		Short: "Show recent searchable activity around a person",
		Long: `Show a search-derived view of recent activity around a Slack user.

With no person, activity is centered on the authenticated user. A person may be
specified by @handle, handle, display name, or Slack user ID. Results combine
messages authored by the person with searchable messages that mention them,
grouped by conversation and visible only through the authenticated user's access.`,
		Example: `  slk activity
  slk activity @alex
  slk activity @alex --since 8h --limit 30
  slk activity U12345678 --since 2026-08-24 --json`,
		Args: argumentValidator(cobra.MaximumNArgs(1)),
	}
	command.Flags().StringVar(&options.since, "since", "24h", "How far back to look (1h, 2d, 2026-08-24)")
	command.Flags().IntVar(&options.limit, "limit", 20, "Maximum number of matching messages (1-100)")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if options.limit < 1 || options.limit > 100 {
			return invalidArgument(cmd, "--limit must be between 1 and 100")
		}
		if strings.TrimSpace(options.since) == "" {
			return invalidArgument(cmd, "--since must not be empty")
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}
		now, err := deps.now()
		if err != nil {
			return err
		}
		cutoff, err := parseCutoffAt(options.since, now)
		if err != nil {
			return invalidArgument(cmd, "--since: "+err.Error())
		}
		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		person := ""
		if len(args) == 1 {
			person = args[0]
		}
		return runActivity(cmd, rootOptions, client, person, cutoff, now, options.limit)
	}
	return command
}

func runActivity(cmd *cobra.Command, rootOptions *rootOptions, client activityClient, person string, cutoff, now time.Time, limit int) error {
	selfID, err := identifySelf(client)
	if err != nil {
		return slackAPIError(err)
	}
	if err := client.BuildUserCache(); err != nil {
		return slackAPIError(fmt.Errorf("loading workspace users: %w", err))
	}
	target, err := resolveActivityPerson(client, selfID, person)
	if err != nil {
		return slackAPIError(err)
	}
	items, err := collectActivity(client, target.ID, cutoff, limit)
	if err != nil {
		return slackAPIError(err)
	}
	groups := groupActivity(items)

	if rootOptions.json {
		out, err := format.FormatJSON(activityJSON(target, selfID, cutoff, groups))
		if err != nil {
			return internalError()
		}
		fmt.Fprintln(cmd.OutOrStdout(), out)
		return nil
	}
	fmt.Fprint(cmd.OutOrStdout(), formatActivity(target, selfID, cutoff, now, groups, client.ResolveUser))
	return nil
}

func resolveActivityPerson(client activityUserResolver, selfID, person string) (*api.User, error) {
	person = strings.TrimPrefix(strings.TrimSpace(person), "@")
	if person == "" || strings.EqualFold(person, "me") {
		user, err := client.GetUserInfo(selfID)
		if err != nil {
			return nil, fmt.Errorf("resolving authenticated user: %w", err)
		}
		return user, nil
	}
	if activityUserIDRe.MatchString(person) {
		user, err := client.GetUserInfo(person)
		if err != nil {
			return nil, fmt.Errorf("resolving user %s: %w", person, err)
		}
		return user, nil
	}
	user, err := client.FindUserByName(person)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func collectActivity(client activitySearcher, userID string, cutoff time.Time, limit int) ([]activityItem, error) {
	mention := "<@" + userID + ">"
	queries := []struct {
		query  string
		reason activityReason
		accept func(api.SearchMatch) bool
	}{
		{query: "from:" + mention, reason: activityAuthored, accept: func(match api.SearchMatch) bool {
			return match.User == userID
		}},
		{query: mention, reason: activityMentioned, accept: func(match api.SearchMatch) bool {
			return strings.Contains(match.Text, mention)
		}},
	}

	items := make([]activityItem, 0, limit)
	indexes := make(map[string]int)
	for _, query := range queries {
		result, err := client.SearchMessages(query.query, limit)
		if err != nil {
			return nil, fmt.Errorf("searching %s activity: %w", query.reason, err)
		}
		if result == nil {
			continue
		}
		for _, match := range result.Messages.Matches {
			occurred := format.TsToTime(match.Ts)
			if occurred.IsZero() || occurred.Before(cutoff) || !query.accept(match) {
				continue
			}
			key := match.Channel.ID + "\x00" + match.Ts
			if index, exists := indexes[key]; exists {
				items[index].Reasons = appendActivityReason(items[index].Reasons, query.reason)
				continue
			}
			indexes[key] = len(items)
			items = append(items, activityItem{Match: match, Reasons: []activityReason{query.reason}})
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Match.Ts > items[j].Match.Ts
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func appendActivityReason(reasons []activityReason, reason activityReason) []activityReason {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func groupActivity(items []activityItem) []activityGroup {
	groups := make([]activityGroup, 0)
	indexes := make(map[string]int)
	for _, item := range items {
		key := item.Match.Channel.ID
		if key == "" {
			key = item.Match.Channel.Name
		}
		if index, exists := indexes[key]; exists {
			groups[index].Items = append(groups[index].Items, item)
			continue
		}
		indexes[key] = len(groups)
		groups = append(groups, activityGroup{Channel: item.Match.Channel, Items: []activityItem{item}})
	}
	return groups
}

func formatActivity(target *api.User, selfID string, cutoff, now time.Time, groups []activityGroup, resolveUser func(string) string) string {
	var out strings.Builder
	handle := target.Name
	if handle == "" {
		handle = target.ID
	}
	fmt.Fprintf(&out, "Activity around @%s", handle)
	if target.ID == selfID {
		out.WriteString(" (me)")
	}
	fmt.Fprintf(&out, "\nSince: %s\n", cutoff.Local().Format("2006-01-02 15:04"))
	out.WriteString("Search-derived; limited to messages visible to the authenticated Slack user.\n")
	if len(groups) == 0 {
		out.WriteString("\nNo searchable activity found.\n")
		return out.String()
	}

	matchCount := 0
	for _, group := range groups {
		matchCount += len(group.Items)
	}
	messageWord, conversationWord := "messages", "conversations"
	if matchCount == 1 {
		messageWord = "message"
	}
	if len(groups) == 1 {
		conversationWord = "conversation"
	}
	fmt.Fprintf(&out, "%d matching %s across %d %s.\n", matchCount, messageWord, len(groups), conversationWord)

	for _, group := range groups {
		latest := group.Items[0].Match
		fmt.Fprintf(&out, "\n%s — latest %s (%s)\n", format.SearchChannelLabel(group.Channel, resolveUser), format.FormatRelativeTime(latest.Ts, now), format.FormatTimestamp(latest.Ts))
		for _, item := range group.Items {
			author := format.SearchAuthorLabel(item.Match, resolveUser, selfID)
			fmt.Fprintf(&out, "  %s · %s · @%s\n", activityReasonLabel(item.Reasons), format.FormatRelativeTime(item.Match.Ts, now), author)
			fmt.Fprintf(&out, "    %s\n", format.ResolveText(item.Match.Text, resolveUser))
			for _, file := range item.Match.Files {
				fmt.Fprintf(&out, "    [%s] %s (%s)%s\n", format.FileCategory(file.Mimetype), file.Name, format.FormatFileSize(file.Size), format.FileDownloadHint(file))
			}
			if command := format.OpenCommand(item.Match.Permalink); command != "" {
				fmt.Fprintf(&out, "    Open: %s\n", command)
			}
		}
	}
	return out.String()
}

func activityReasonLabel(reasons []activityReason) string {
	labels := make([]string, len(reasons))
	for i, reason := range reasons {
		labels[i] = string(reason)
	}
	return strings.Join(labels, ", ")
}

type activityPayload struct {
	OK            bool                       `json:"ok"`
	SearchDerived bool                       `json:"search_derived"`
	Person        activityPersonJSON         `json:"person"`
	Since         string                     `json:"since"`
	Conversations []activityConversationJSON `json:"conversations"`
}

type activityPersonJSON struct {
	UserID      string `json:"user_id"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	IsSelf      bool   `json:"is_self"`
}

type activityConversationJSON struct {
	Channel  api.SearchChannel  `json:"channel"`
	LatestTs string             `json:"latest_ts"`
	Items    []activityItemJSON `json:"items"`
}

type activityItemJSON struct {
	Reasons []activityReason       `json:"reasons"`
	Message format.SearchMatchJSON `json:"message"`
}

func activityJSON(target *api.User, selfID string, cutoff time.Time, groups []activityGroup) activityPayload {
	conversations := make([]activityConversationJSON, 0, len(groups))
	for _, group := range groups {
		items := make([]activityItemJSON, 0, len(group.Items))
		for _, item := range group.Items {
			items = append(items, activityItemJSON{
				Reasons: item.Reasons,
				Message: format.SearchMatchToJSON(item.Match, selfID),
			})
		}
		conversations = append(conversations, activityConversationJSON{
			Channel: group.Channel, LatestTs: group.Items[0].Match.Ts, Items: items,
		})
	}
	displayName := target.Profile.DisplayName
	if displayName == "" {
		displayName = target.Name
	}
	return activityPayload{
		OK: true, SearchDerived: true,
		Person: activityPersonJSON{UserID: target.ID, Handle: target.Name, DisplayName: displayName, IsSelf: target.ID == selfID},
		Since:  cutoff.UTC().Format(time.RFC3339), Conversations: conversations,
	}
}
