package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

type readOptions struct {
	limit  int
	after  string
	before string
	around string
}

var relativeTimeRe = regexp.MustCompile(`^(\d+)([smhd])$`)

func newReadCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &readOptions{limit: 25}
	command := &cobra.Command{
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
		Args: argumentValidator(cobra.ExactArgs(1)),
	}
	command.Flags().IntVar(&options.limit, "limit", 25, "Maximum number of messages to retrieve")
	command.Flags().StringVar(&options.after, "after", "", "Show messages after this time (2024-01-15, 1h, 2d)")
	command.Flags().StringVar(&options.before, "before", "", "Show messages before this time")
	command.Flags().StringVar(&options.around, "around", "", "Show messages around this Slack timestamp")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if options.around != "" && (options.after != "" || options.before != "") {
			return conflictingOptions(cmd, "--around is mutually exclusive with --after and --before")
		}
		if err := checkContext(cmd.Context()); err != nil {
			return err
		}
		now, err := deps.now()
		if err != nil {
			return err
		}
		oldest, err := parseTimeArgAt(options.after, now)
		if err != nil {
			return invalidArgument(cmd, "--after: "+err.Error())
		}
		latest, err := parseTimeArgAt(options.before, now)
		if err != nil {
			return invalidArgument(cmd, "--before: "+err.Error())
		}

		client, err := getClient(cmd, deps)
		if err != nil {
			return err
		}
		selfID, err := identifySelf(client)
		if err != nil {
			return slackAPIError(err)
		}
		if err := client.BuildUserCache(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: user cache unavailable: %v\n", err)
		}
		channelID, channelName, err := resolveTarget(client, args[0])
		if err != nil {
			return slackAPIError(err)
		}

		var messages []api.Message
		if options.around != "" {
			halfBefore := options.limit / 2
			halfAfter := options.limit - halfBefore
			before, err := client.GetHistory(channelID, halfBefore, "", options.around)
			if err != nil {
				return slackAPIError(err)
			}
			after, err := client.GetHistoryAfter(channelID, halfAfter+1, options.around)
			if err != nil {
				return slackAPIError(err)
			}
			seen := make(map[string]bool, len(before)+len(after))
			for _, message := range append(before, after...) {
				if !seen[message.Ts] {
					seen[message.Ts] = true
					messages = append(messages, message)
				}
			}
			sort.Slice(messages, func(i, j int) bool { return messages[i].Ts > messages[j].Ts })
			if len(messages) > options.limit {
				messages = messages[:options.limit]
			}
		} else {
			messages, err = client.GetHistory(channelID, options.limit, oldest, latest)
			if err != nil {
				return slackAPIError(err)
			}
		}
		reverseMessages(messages)

		if rootOptions.json {
			out, err := format.FormatJSON(map[string]interface{}{
				"ok": true, "channel": channelName,
				"messages": format.MessagesToJSON(messages, client.ResolveUser, selfID),
			})
			if err != nil {
				return internalError()
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), format.FormatMessages(messages, channelName, client.ResolveUser, selfID))
		return nil
	}
	return command
}

type targetResolver interface {
	FindDMByUser(username string) (*api.Channel, error)
	FindDMByUserID(userID string) (*api.Channel, error)
	FindChannelByName(name string) (*api.Channel, error)
	ResolveUser(userID string) string
}

func resolveTarget(client targetResolver, target string) (channelID, channelName string, err error) {
	if strings.HasPrefix(target, "@") {
		channel, err := client.FindDMByUser(target[1:])
		if err != nil {
			return "", "", err
		}
		return channel.ID, "@" + client.ResolveUser(channel.User), nil
	}
	if matched, _ := regexp.MatchString(`^U[A-Z0-9]{8,}$`, target); matched {
		channel, err := client.FindDMByUserID(target)
		if err != nil {
			return "", "", err
		}
		return channel.ID, "@" + client.ResolveUser(target), nil
	}
	if len(target) >= 9 && (strings.HasPrefix(target, "C") || strings.HasPrefix(target, "G") || strings.HasPrefix(target, "D")) {
		return target, target, nil
	}
	channel, err := client.FindChannelByName(target)
	if err != nil {
		return "", "", err
	}
	return channel.ID, channel.Name, nil
}

func parseTimeArg(val string) (string, error) {
	return parseTimeArgAt(val, time.Now())
}

func parseCutoffAt(val string, now time.Time) (time.Time, error) {
	epoch, err := parseTimeArgAt(val, now)
	if err != nil {
		return time.Time{}, err
	}
	if epoch == "" {
		return time.Time{}, nil
	}
	seconds, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time")
	}
	return time.Unix(seconds, 0), nil
}

func parseTimeArgAt(val string, now time.Time) (string, error) {
	if val == "" {
		return "", nil
	}
	if match := relativeTimeRe.FindStringSubmatch(val); len(match) == 3 {
		num, _ := strconv.Atoi(match[1])
		var duration time.Duration
		switch match[2] {
		case "s":
			duration = time.Duration(num) * time.Second
		case "m":
			duration = time.Duration(num) * time.Minute
		case "h":
			duration = time.Duration(num) * time.Hour
		case "d":
			duration = time.Duration(num) * 24 * time.Hour
		}
		return fmt.Sprintf("%d", now.Add(-duration).Unix()), nil
	}
	if _, err := strconv.ParseFloat(val, 64); err == nil && len(val) >= 9 {
		timestamp, _ := strconv.ParseInt(val, 10, 64)
		return fmt.Sprintf("%d", timestamp), nil
	}
	for _, layout := range []string{
		time.RFC3339, "2006-01-02T15:04Z", "2006-01-02T15:04:05",
		"2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
	} {
		if parsed, err := time.Parse(layout, val); err == nil {
			return fmt.Sprintf("%d", parsed.Unix()), nil
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if parsed, err := time.Parse(layout, val); err == nil {
			timestamp := time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), 0, time.Local)
			return fmt.Sprintf("%d", timestamp.Unix()), nil
		}
	}
	return "", fmt.Errorf("unrecognized time format (use 2024-01-15, 2024-01-15T14:00, 14:00, 1h, 2d, or unix timestamp)")
}

func reverseMessages(messages []api.Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}
