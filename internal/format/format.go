package format

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/leezenn/slk/internal/api"
)

var (
	userMentionRe    = regexp.MustCompile(`<@(U[A-Z0-9]+)>`)
	channelMentionRe = regexp.MustCompile(`<#(C[A-Z0-9]+)\|([^>]+)>`)
	urlLinkRe        = regexp.MustCompile(`<(https?://[^|>]+)\|([^>]+)>`)
	bareURLRe        = regexp.MustCompile(`<(https?://[^>]+)>`)
)

// ResolveText replaces Slack markup with readable text.
func ResolveText(text string, resolveUser func(string) string) string {
	// Resolve user mentions: <@U12345> -> @display_name
	text = userMentionRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := userMentionRe.FindStringSubmatch(match)
		if len(parts) >= 2 {
			return "@" + resolveUser(parts[1])
		}
		return match
	})

	// Resolve channel mentions: <#C12345|channel-name> -> #channel-name
	text = channelMentionRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := channelMentionRe.FindStringSubmatch(match)
		if len(parts) >= 3 {
			return "#" + parts[2]
		}
		return match
	})

	// Resolve URL links: <http://example.com|Display Text> -> Display Text
	text = urlLinkRe.ReplaceAllString(text, "$2")

	// Resolve bare URLs: <http://example.com> -> http://example.com
	text = bareURLRe.ReplaceAllString(text, "$1")

	// Clean up other Slack formatting
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")

	return text
}

// TsToTime converts a Slack timestamp to time.Time.
func TsToTime(ts string) time.Time {
	parts := strings.Split(ts, ".")
	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: malformed timestamp: %s\n", ts)
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// FormatTimestamp formats a Slack timestamp for human display.
func FormatTimestamp(ts string) string {
	t := TsToTime(ts)
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// FormatFileSize formats bytes into a human-readable size.
func FormatFileSize(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(size)/float64(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(size)/float64(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(size)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", size)
	}
}

// FormatMessages formats messages for human-readable output.
func FormatMessages(msgs []api.Message, channelName string, resolveUser func(string) string) string {
	if len(msgs) == 0 {
		return "No messages found.\n"
	}

	var b strings.Builder

	for _, msg := range msgs {
		// Skip messages without text content (subtypes like join/leave)
		if msg.Text == "" {
			continue
		}

		// Determine display name
		userName := resolveUser(msg.User)
		if msg.User == "" && msg.BotID != "" {
			userName = msg.Username
			if userName == "" {
				userName = "bot"
			}
		}

		ts := FormatTimestamp(msg.Ts)
		resolvedText := ResolveText(msg.Text, resolveUser)

		if channelName != "" {
			prefix := "#"
			if strings.HasPrefix(channelName, "@") {
				prefix = ""
			}
			fmt.Fprintf(&b, "%s%s \u2014 %s\n", prefix, channelName, ts)
		} else {
			fmt.Fprintf(&b, "%s\n", ts)
		}
		fmt.Fprintf(&b, "  @%s: %s\n", userName, resolvedText)

		// Reactions
		if len(msg.Reactions) > 0 {
			// Group by user: user -> list of emoji names
			userReactions := make(map[string][]string)
			var userOrder []string
			seen := make(map[string]bool)
			for _, r := range msg.Reactions {
				for _, uid := range r.Users {
					if !seen[uid] {
						seen[uid] = true
						userOrder = append(userOrder, uid)
					}
					userReactions[uid] = append(userReactions[uid], r.Name)
				}
			}
			var parts []string
			for _, uid := range userOrder {
				name := resolveUser(uid)
				emojis := userReactions[uid]
				var emojiStrs []string
				for _, e := range emojis {
					emojiStrs = append(emojiStrs, ":"+e+":")
				}
				parts = append(parts, fmt.Sprintf("%s @%s", strings.Join(emojiStrs, " "), name))
			}
			fmt.Fprintf(&b, "    %s\n", strings.Join(parts, "  "))
		}

		// Files
		for _, f := range msg.Files {
			fmt.Fprintf(&b, "    [file] %s (%s)\n", f.Name, FormatFileSize(f.Size))
		}

		// Thread indicator
		if msg.ReplyCount > 0 && msg.ThreadTs == msg.Ts {
			if msg.LatestReply != "" {
				latestTime := TsToTime(msg.LatestReply).Local().Format("15:04")
				fmt.Fprintf(&b, "    [%d replies, latest: %s, ts: %s]\n", msg.ReplyCount, latestTime, msg.Ts)
			} else {
				fmt.Fprintf(&b, "    [%d replies, ts: %s]\n", msg.ReplyCount, msg.Ts)
			}
		}

		b.WriteString("\n")
	}

	return b.String()
}

// FormatChannels formats channel list for human-readable output.
func FormatChannels(channels []api.Channel, resolveUser func(string) string) string {
	if len(channels) == 0 {
		return "No channels found.\n"
	}

	var b strings.Builder

	for _, ch := range channels {
		chType := channelType(ch)
		name := ch.Name
		if ch.IsIM && resolveUser != nil {
			name = "@" + resolveUser(ch.User)
		}

		fmt.Fprintf(&b, "%-30s  %-8s  %4d members", name, chType, ch.NumMembers)
		if ch.Topic.Value != "" {
			topic := ch.Topic.Value
			if len(topic) > 60 {
				topic = topic[:57] + "..."
			}
			fmt.Fprintf(&b, "  | %s", topic)
		}
		b.WriteString("\n")
	}

	return b.String()
}

var userIDRe = regexp.MustCompile(`^U[A-Z0-9]{8,}$`)

// isDMChannel returns true when Slack's search API returns a user ID as
// the channel name, which happens for DM conversations.
func isDMChannel(name string) bool {
	return userIDRe.MatchString(name)
}

func channelType(ch api.Channel) string {
	switch {
	case ch.IsIM:
		return "dm"
	case ch.IsMpIM:
		return "mpim"
	case ch.IsPrivate:
		return "private"
	default:
		return "public"
	}
}

// FormatUsers formats user list for human-readable output.
func FormatUsers(users []api.User) string {
	if len(users) == 0 {
		return "No users found.\n"
	}

	var b strings.Builder

	for _, u := range users {
		if u.Deleted || u.IsBot {
			continue
		}
		displayName := u.Profile.DisplayName
		if displayName == "" {
			displayName = u.Name
		}

		// Presence indicator (only populated with --status)
		if u.Presence == "active" {
			fmt.Fprintf(&b, "* ")
		} else if u.Presence == "away" {
			fmt.Fprintf(&b, "  ")
		}

		fmt.Fprintf(&b, "%-25s  %-30s  %-30s", displayName, u.RealName, u.Profile.Title)

		// Status: emoji + text
		status := u.Profile.StatusEmoji
		if u.Profile.StatusText != "" {
			if status != "" {
				status += " "
			}
			status += u.Profile.StatusText
		}
		if status != "" {
			fmt.Fprintf(&b, "  %s", status)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// FormatSearchResults formats search results for human-readable output.
func FormatSearchResults(result *api.SearchResult, resolveUser func(string) string) string {
	if result == nil || len(result.Messages.Matches) == 0 {
		return "No results found.\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d results:\n\n", result.Messages.Total)

	for _, m := range result.Messages.Matches {
		ts := FormatTimestamp(m.Ts)
		text := ResolveText(m.Text, resolveUser)
		chName := m.Channel.Name
		if isDMChannel(chName) {
			chName = "@" + resolveUser(chName)
		} else {
			chName = "#" + chName
		}
		fmt.Fprintf(&b, "%s \u2014 %s\n", chName, ts)
		author := resolveUser(m.User)
		if author == m.User && m.Username != "" {
			author = m.Username
		}
		fmt.Fprintf(&b, "  @%s: %s\n\n", author, text)
	}

	return b.String()
}

// JSONOutput wraps data for JSON output.
type JSONOutput struct {
	OK   bool        `json:"ok"`
	Data interface{} `json:"data,omitempty"`
}

// FormatJSON marshals data as indented JSON.
func FormatJSON(data interface{}) (string, error) {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// MessageJSON is the JSON representation of a message.
type MessageJSON struct {
	User       string         `json:"user"`
	UserID     string         `json:"user_id"`
	Text       string         `json:"text"`
	Ts         string         `json:"ts"`
	Timestamp  string         `json:"timestamp"`
	ThreadTs   string         `json:"thread_ts,omitempty"`
	ReplyCount int            `json:"reply_count,omitempty"`
	Reactions  []api.Reaction `json:"reactions,omitempty"`
	Files      []api.File     `json:"files,omitempty"`
}

// MessagesToJSON converts messages to JSON-friendly structs.
func MessagesToJSON(msgs []api.Message, resolveUser func(string) string) []MessageJSON {
	var out []MessageJSON
	for _, msg := range msgs {
		if msg.Text == "" {
			continue
		}
		userName := resolveUser(msg.User)
		if msg.User == "" && msg.BotID != "" {
			userName = msg.Username
			if userName == "" {
				userName = "bot"
			}
		}
		t := TsToTime(msg.Ts)
		out = append(out, MessageJSON{
			User:       userName,
			UserID:     msg.User,
			Text:       msg.Text,
			Ts:         msg.Ts,
			Timestamp:  t.UTC().Format(time.RFC3339),
			ThreadTs:   msg.ThreadTs,
			ReplyCount: msg.ReplyCount,
			Reactions:  msg.Reactions,
			Files:      msg.Files,
		})
	}
	return out
}
