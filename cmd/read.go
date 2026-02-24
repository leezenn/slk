package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var (
	readLimit  int
	readAfter  string
	readBefore string
	readAround string
)

var relativeTimeRe = regexp.MustCompile(`^(\d+)([smhd])$`)

var readCmd = &cobra.Command{
	Use:   "read <channel-or-user>",
	Short: "Read messages from a channel or DM",
	Long: `Read messages from a Slack channel or DM conversation.

Target can be a channel name (e.g., general), channel ID (e.g., C12345),
or a username prefixed with @ (e.g., @john) for DMs.

Time filters accept absolute dates (2024-01-15, 2024-01-15T14:00) or
relative durations (1h, 2d, 30m, 60s).`,
	Example: `  slk read general                    # Recent messages from #general
  slk read general --limit 50         # Last 50 messages
  slk read @john                      # DMs with john
  slk read general --after 1d         # Messages from last 24 hours
  slk read general --after 2024-01-15 # Messages since Jan 15
  slk read general --around 1705312325.000100 --limit 10  # Messages around a timestamp`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]

		result, err := auth.GetToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		client := api.NewClient(result.Token)

		// Build user cache for mention resolution
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: user cache unavailable: %v\n", err)
		}

		// Resolve target to channel ID
		channelID, channelName, err := resolveTarget(client, target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		var msgs []api.Message

		if readAround != "" {
			if readAfter != "" || readBefore != "" {
				fmt.Fprintln(os.Stderr, "Error: --around is mutually exclusive with --after and --before")
				os.Exit(1)
			}

			halfBefore := readLimit / 2
			halfAfter := readLimit - halfBefore // gets the extra 1 when limit is odd

			// Fetch messages before (and excluding) the target ts
			before, err := client.GetHistory(channelID, halfBefore, "", readAround)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching messages before: %v\n", err)
				os.Exit(1)
			}

			// Fetch messages from the target ts onward (inclusive)
			after, err := client.GetHistoryAfter(channelID, halfAfter+1, readAround)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching messages after: %v\n", err)
				os.Exit(1)
			}

			// Deduplicate by ts and merge
			seen := make(map[string]bool, len(before)+len(after))
			for _, m := range before {
				if !seen[m.Ts] {
					seen[m.Ts] = true
					msgs = append(msgs, m)
				}
			}
			for _, m := range after {
				if !seen[m.Ts] {
					seen[m.Ts] = true
					msgs = append(msgs, m)
				}
			}

			// Both API calls return newest-first; sort all by ts descending
			// so the subsequent reverseMessages gives chronological order
			sort.Slice(msgs, func(i, j int) bool {
				return msgs[i].Ts > msgs[j].Ts
			})

			// Cap to limit
			if len(msgs) > readLimit {
				msgs = msgs[:readLimit]
			}
		} else {
			// Parse time filters
			oldest, err := parseTimeArg(readAfter)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing --after: %v\n", err)
				os.Exit(1)
			}
			latest, err := parseTimeArg(readBefore)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing --before: %v\n", err)
				os.Exit(1)
			}

			msgs, err = client.GetHistory(channelID, readLimit, oldest, latest)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}

		// Reverse to chronological order (API returns newest first)
		reverseMessages(msgs)

		if jsonOutput {
			jsonMsgs := format.MessagesToJSON(msgs, client.ResolveUser)
			out, err := format.FormatJSON(map[string]interface{}{
				"ok":       true,
				"channel":  channelName,
				"messages": jsonMsgs,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(out)
			return
		}

		fmt.Print(format.FormatMessages(msgs, channelName, client.ResolveUser))
	},
}

func resolveTarget(client *api.Client, target string) (channelID, channelName string, err error) {
	// Username/display name -> DM
	if strings.HasPrefix(target, "@") {
		username := target[1:]
		ch, err := client.FindDMByUser(username)
		if err != nil {
			return "", "", err
		}
		displayName := client.ResolveUser(ch.User)
		return ch.ID, "@" + displayName, nil
	}

	// User ID (U-prefixed, uppercase alphanumeric) -> DM
	if matched, _ := regexp.MatchString(`^U[A-Z0-9]{8,}$`, target); matched {
		ch, err := client.FindDMByUserID(target)
		if err != nil {
			return "", "", err
		}
		displayName := client.ResolveUser(target)
		return ch.ID, "@" + displayName, nil
	}

	// Channel ID
	if strings.HasPrefix(target, "C") || strings.HasPrefix(target, "G") || strings.HasPrefix(target, "D") {
		if len(target) >= 9 {
			return target, target, nil
		}
	}

	// Channel name lookup
	ch, err := client.FindChannelByName(target)
	if err != nil {
		return "", "", err
	}
	return ch.ID, ch.Name, nil
}

func parseTimeArg(val string) (string, error) {
	if val == "" {
		return "", nil
	}

	// Relative time: 1h, 2d, 30m, 60s
	if m := relativeTimeRe.FindStringSubmatch(val); len(m) == 3 {
		num, _ := strconv.Atoi(m[1])
		var dur time.Duration
		switch m[2] {
		case "s":
			dur = time.Duration(num) * time.Second
		case "m":
			dur = time.Duration(num) * time.Minute
		case "h":
			dur = time.Duration(num) * time.Hour
		case "d":
			dur = time.Duration(num) * 24 * time.Hour
		}
		ts := time.Now().Add(-dur)
		return fmt.Sprintf("%d", ts.Unix()), nil
	}

	// Unix timestamp (raw seconds)
	if _, err := strconv.ParseFloat(val, 64); err == nil && len(val) >= 9 {
		// Looks like a unix timestamp (9+ digits). ParseFloat handles both int and float.
		ts, _ := strconv.ParseInt(val, 10, 64)
		return fmt.Sprintf("%d", ts), nil
	}

	// ISO 8601 with timezone: 2024-01-15T14:00:00Z or 2024-01-15T14:00:00+00:00
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return fmt.Sprintf("%d", t.Unix()), nil
	}
	// Z suffix without seconds: 2024-01-15T14:00Z
	if t, err := time.Parse("2006-01-02T15:04Z", val); err == nil {
		return fmt.Sprintf("%d", t.Unix()), nil
	}

	// Absolute: 2024-01-15T14:00:00 or 2024-01-15T14:00
	if t, err := time.Parse("2006-01-02T15:04:05", val); err == nil {
		return fmt.Sprintf("%d", t.Unix()), nil
	}
	if t, err := time.Parse("2006-01-02T15:04", val); err == nil {
		return fmt.Sprintf("%d", t.Unix()), nil
	}

	// Absolute: 2024-01-15 14:00:00 or 2024-01-15 14:00 (space separator)
	if t, err := time.Parse("2006-01-02 15:04:05", val); err == nil {
		return fmt.Sprintf("%d", t.Unix()), nil
	}
	if t, err := time.Parse("2006-01-02 15:04", val); err == nil {
		return fmt.Sprintf("%d", t.Unix()), nil
	}

	// Absolute: 2024-01-15
	if t, err := time.Parse("2006-01-02", val); err == nil {
		return fmt.Sprintf("%d", t.Unix()), nil
	}

	// Time only (today): 14:00 or 14:00:00
	if t, err := time.Parse("15:04:05", val); err == nil {
		now := time.Now()
		ts := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.Local)
		return fmt.Sprintf("%d", ts.Unix()), nil
	}
	if t, err := time.Parse("15:04", val); err == nil {
		now := time.Now()
		ts := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
		return fmt.Sprintf("%d", ts.Unix()), nil
	}

	return "", fmt.Errorf("unrecognized time format: %s (use 2024-01-15, 2024-01-15T14:00, \"2024-01-15 14:00\", 14:00, 1h, 2d, or unix timestamp)", val)
}

func reverseMessages(msgs []api.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

func init() {
	readCmd.Flags().IntVar(&readLimit, "limit", 25, "Maximum number of messages to retrieve")
	readCmd.Flags().StringVar(&readAfter, "after", "", "Show messages after this time (2024-01-15, 1h, 2d)")
	readCmd.Flags().StringVar(&readBefore, "before", "", "Show messages before this time")
	readCmd.Flags().StringVar(&readAround, "around", "", "Show messages around this Slack timestamp")
	rootCmd.AddCommand(readCmd)
}
