package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

var (
	notesSince  string
	notesEmoji  string
	notesDryRun bool
	notesDir    string
	notesFormat string
)

// notesState tracks which messages have already been captured.
type notesState struct {
	Captured map[string]string `json:"captured"` // "channelID:ts" -> ISO timestamp
}

// noteContext holds context messages to prepend to a note.
type noteContext struct {
	parent   *api.Message // thread parent
	previous *api.Message // preceding message
}

// noteOutput is the JSON representation of a captured note.
type noteOutput struct {
	Channel   string `json:"channel"`
	User      string `json:"user"`
	Text      string `json:"text"`
	Ts        string `json:"ts"`
	Timestamp string `json:"timestamp"`
	File      string `json:"file"`
}

var notesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Capture Slack messages you reacted to as markdown notes",
	Long: `Find Slack messages the authenticated user reacted to with a specific emoji
and save them locally.

Output: <dir>/notes.jsonl (default) or <dir>/*.md
State:  <dir>/.slk-state.json

Directory: --dir > SLK_NOTES_DIR > ~/Documents/notes/slack/
Format:  --format > SLK_NOTES_FORMAT > jsonl`,
	Example: `  slk notes                          # Capture new writing_hand notes
  slk notes --dry-run                 # Preview without saving
  slk notes --emoji eyes --since 30d  # Different emoji, longer window
  slk notes --format md                # Save as individual .md files
  slk notes --dir ~/work/notes        # Custom directory`,
	Run: runNotes,
}

func runNotes(cmd *cobra.Command, args []string) {
	// Parse --since into a cutoff time
	sinceEpoch, err := parseTimeArg(notesSince)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing --since: %v\n", err)
		os.Exit(1)
	}
	var cutoff time.Time
	if sinceEpoch != "" {
		var sec int64
		fmt.Sscanf(sinceEpoch, "%d", &sec)
		cutoff = time.Unix(sec, 0)
	}

	// Auth
	result, err := auth.GetToken()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	client := api.NewClient(result.Token)
	if err := client.Identify(); err != nil {
		fmt.Fprintf(os.Stderr, "Error identifying user: %v\n", err)
		os.Exit(1)
	}
	if err := client.BuildUserCache(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: user cache unavailable: %v\n", err)
	}

	// Build channel cache: ID -> name
	channels, err := client.ListChannels("public_channel,private_channel,mpim,im", 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing channels: %v\n", err)
		os.Exit(1)
	}
	channelMap := make(map[string]string, len(channels))
	for _, ch := range channels {
		name := ch.Name
		if ch.IsIM {
			name = "dm-" + client.ResolveUser(ch.User)
		}
		channelMap[ch.ID] = name
	}

	// Fetch all reacted items
	items, err := client.ReactionsList(0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching reactions: %v\n", err)
		os.Exit(1)
	}

	// Filter items
	var filtered []api.ReactionsListItem
	for _, item := range items {
		if item.Type != "message" {
			continue
		}

		// Time filter
		msgTime := format.TsToTime(item.Message.Ts)
		if !cutoff.IsZero() && msgTime.Before(cutoff) {
			continue
		}

		// Emoji filter: check if message has a matching reaction
		if !hasMatchingReaction(item.Message.Reactions, notesEmoji, client.SelfID()) {
			continue
		}

		filtered = append(filtered, item)
	}

	// Resolve notes directory: --dir flag > SLK_NOTES_DIR env > ~/.notes
	resolvedDir := notesDir
	if resolvedDir == "" {
		resolvedDir = os.Getenv("SLK_NOTES_DIR")
	}
	if resolvedDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot determine home directory: %v\n", err)
			os.Exit(1)
		}
		resolvedDir = filepath.Join(home, "Documents", "notes", "slack")
	}

	// Resolve format: --format flag > SLK_NOTES_FORMAT env > md
	resolvedFormat := notesFormat
	if resolvedFormat == "" {
		resolvedFormat = os.Getenv("SLK_NOTES_FORMAT")
	}
	if resolvedFormat == "" {
		resolvedFormat = "jsonl"
	}
	if resolvedFormat != "md" && resolvedFormat != "jsonl" {
		fmt.Fprintf(os.Stderr, "Error: --format must be 'md' or 'jsonl', got '%s'\n", resolvedFormat)
		os.Exit(1)
	}

	// Load state
	statePath := filepath.Join(resolvedDir, ".slk-state.json")
	state := loadState(statePath)

	// Filter already captured
	var newItems []api.ReactionsListItem
	for _, item := range filtered {
		key := item.Channel + ":" + item.Message.Ts
		if _, seen := state.Captured[key]; !seen {
			newItems = append(newItems, item)
		}
	}

	// Fetch context messages for each new item
	contextMessages := make(map[int]*noteContext, len(newItems))
	for i, item := range newItems {
		msg := item.Message
		isReply := msg.ThreadTs != "" && msg.ThreadTs != msg.Ts
		isShort := len(strings.TrimSpace(msg.Text)) < 80
		ctx := &noteContext{}
		hasContext := false

		// Thread reply — fetch parent message
		if isReply {
			replies, err := client.GetReplies(item.Channel, msg.ThreadTs, 1)
			if err == nil && len(replies) > 0 {
				ctx.parent = &replies[0]
				hasContext = true
			} else if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not fetch thread context: %v\n", err)
			}
		}

		// Short message — fetch preceding message (from thread or channel)
		if isShort {
			if isReply {
				// Get previous reply in thread (message just before this one)
				replies, err := client.GetReplies(item.Channel, msg.ThreadTs, 0)
				if err == nil {
					for j, r := range replies {
						if r.Ts == msg.Ts && j > 0 {
							prev := replies[j-1]
							// Don't duplicate parent as previous
							if ctx.parent == nil || prev.Ts != ctx.parent.Ts {
								ctx.previous = &prev
								hasContext = true
							}
							break
						}
					}
				} else {
					fmt.Fprintf(os.Stderr, "Warning: could not fetch thread replies: %v\n", err)
				}
			} else {
				// Get previous message in channel
				history, err := client.GetHistory(item.Channel, 1, "", msg.Ts)
				if err == nil && len(history) > 0 {
					ctx.previous = &history[0]
					hasContext = true
				} else if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not fetch channel history: %v\n", err)
				}
			}
		}

		if hasContext {
			contextMessages[i] = ctx
		}
	}

	// Nothing to do
	if len(newItems) == 0 {
		if jsonOutput {
			out, err := format.FormatJSON(map[string]interface{}{
				"ok":       true,
				"captured": 0,
				"notes":    []noteOutput{},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error formatting JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(out)
		} else {
			fmt.Println("No new notes found.")
		}
		return
	}

	// Dry run
	if notesDryRun {
		if jsonOutput {
			notes := buildNoteOutputs(newItems, channelMap, client)
			out, err := format.FormatJSON(map[string]interface{}{
				"ok":       true,
				"captured": len(notes),
				"notes":    notes,
				"dry_run":  true,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error formatting JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(out)
		} else {
			fmt.Printf("Would capture %d notes:\n", len(newItems))
			for _, item := range newItems {
				chName := resolveChannelName(item.Channel, channelMap)
				userName := client.ResolveUser(item.Message.User)
				ts := format.FormatTimestamp(item.Message.Ts)
				text := format.ResolveText(item.Message.Text, client.ResolveUser)
				text = truncate(text, 60)
				fmt.Printf("  @%s in #%s (%s): %s\n", userName, chName, ts, text)
			}
		}
		return
	}

	// Ensure notes directory exists
	if err := os.MkdirAll(resolvedDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating notes directory: %v\n", err)
		os.Exit(1)
	}

	// Save notes
	var outputs []noteOutput
	now := time.Now().UTC()

	// For JSONL: open file in append mode
	var jsonlFile *os.File
	if resolvedFormat == "jsonl" {
		jsonlPath := filepath.Join(resolvedDir, "notes.jsonl")
		var err error
		jsonlFile, err = os.OpenFile(jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", jsonlPath, err)
			os.Exit(1)
		}
		defer jsonlFile.Close()
	}

	for i, item := range newItems {
		chName := resolveChannelName(item.Channel, channelMap)
		userName := client.ResolveUser(item.Message.User)
		msgTime := format.TsToTime(item.Message.Ts).Local()
		text := format.ResolveText(item.Message.Text, client.ResolveUser)

		// Build context text for JSONL
		var contextText string
		if ctx := contextMessages[i]; ctx != nil {
			if ctx.parent != nil {
				contextText += fmt.Sprintf("[%s, thread]: %s", client.ResolveUser(ctx.parent.User), format.ResolveText(ctx.parent.Text, client.ResolveUser))
			}
			if ctx.previous != nil {
				if contextText != "" {
					contextText += "\n"
				}
				contextText += fmt.Sprintf("[%s, previous]: %s", client.ResolveUser(ctx.previous.User), format.ResolveText(ctx.previous.Text, client.ResolveUser))
			}
		}

		filename := fmt.Sprintf("%s-%s.md",
			msgTime.Format("2006-01-02-150405"),
			sanitizeFilename(chName),
		)

		if resolvedFormat == "jsonl" {
			record := map[string]interface{}{
				"channel":   chName,
				"user":      userName,
				"text":      text,
				"ts":        item.Message.Ts,
				"timestamp": msgTime.UTC().Format(time.RFC3339),
				"captured":  now.Format(time.RFC3339),
			}
			if contextText != "" {
				record["context"] = contextText
			}
			if len(item.Message.Files) > 0 {
				var fileNames []string
				for _, f := range item.Message.Files {
					fileNames = append(fileNames, f.Name)
				}
				record["files"] = fileNames
			}
			line, err := json.Marshal(record)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshaling note: %v\n", err)
				continue
			}
			jsonlFile.Write(line)
			jsonlFile.WriteString("\n")
		} else {
			filePath := filepath.Join(resolvedDir, filename)
			content := buildNoteContent(userName, chName, text, item.Message.Files, msgTime, contextMessages[i], client.ResolveUser)
			if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", filePath, err)
				continue
			}
		}

		// Update state
		key := item.Channel + ":" + item.Message.Ts
		state.Captured[key] = now.Format(time.RFC3339)

		outputs = append(outputs, noteOutput{
			Channel:   chName,
			User:      userName,
			Text:      item.Message.Text,
			Ts:        item.Message.Ts,
			Timestamp: msgTime.UTC().Format(time.RFC3339),
			File:      filename,
		})
	}

	// Save state
	saveState(statePath, state)

	// Output
	if jsonOutput {
		out, err := format.FormatJSON(map[string]interface{}{
			"ok":       true,
			"captured": len(outputs),
			"notes":    outputs,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(out)
	} else {
		if resolvedFormat == "jsonl" {
			fmt.Printf("Appended %d notes to %s/notes.jsonl\n", len(outputs), resolvedDir)
		} else {
			fmt.Printf("Captured %d new notes:\n", len(outputs))
			for _, n := range outputs {
				text := format.ResolveText(n.Text, client.ResolveUser)
				text = truncate(text, 60)
				fmt.Printf("  %s/%s — @%s: %s\n", resolvedDir, n.File, n.User, text)
			}
		}
	}
}

func hasMatchingReaction(reactions []api.Reaction, emoji, selfID string) bool {
	for _, r := range reactions {
		if !strings.HasPrefix(r.Name, emoji) {
			continue
		}
		for _, u := range r.Users {
			if u == selfID {
				return true
			}
		}
	}
	return false
}

func resolveChannelName(channelID string, channelMap map[string]string) string {
	if name, ok := channelMap[channelID]; ok {
		return name
	}
	return channelID
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ToLower(name)
	return name
}

func truncate(s string, max int) string {
	// Truncate at first newline
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max-3]) + "..."
	}
	return s
}

func buildNoteContent(userName, channelName, text string, files []api.File, msgTime time.Time, ctx *noteContext, resolveUser func(string) string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "@%s in #%s\n\n", userName, channelName)

	// Context: thread parent
	if ctx != nil && ctx.parent != nil {
		parentUser := resolveUser(ctx.parent.User)
		parentText := format.ResolveText(ctx.parent.Text, resolveUser)
		fmt.Fprintf(&b, "[@%s, thread]:\n", parentUser)
		for _, line := range strings.Split(parentText, "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
		b.WriteString("\n")
	}

	// Context: previous message
	if ctx != nil && ctx.previous != nil {
		prevUser := resolveUser(ctx.previous.User)
		prevText := format.ResolveText(ctx.previous.Text, resolveUser)
		fmt.Fprintf(&b, "[@%s, previous]:\n", prevUser)
		for _, line := range strings.Split(prevText, "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
		b.WriteString("\n")
	}

	// Blockquote the message text
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		fmt.Fprintf(&b, "> %s\n", line)
	}

	// Files
	if len(files) > 0 {
		b.WriteString("\n")
		for _, f := range files {
			fmt.Fprintf(&b, "[file] %s (%s)\n", f.Name, format.FormatFileSize(f.Size))
		}
	}

	fmt.Fprintf(&b, "\n%s\n", msgTime.Format("2006-01-02 15:04"))

	return b.String()
}

func buildNoteOutputs(items []api.ReactionsListItem, channelMap map[string]string, client *api.Client) []noteOutput {
	var out []noteOutput
	for _, item := range items {
		chName := resolveChannelName(item.Channel, channelMap)
		userName := client.ResolveUser(item.Message.User)
		msgTime := format.TsToTime(item.Message.Ts).Local()
		filename := fmt.Sprintf("%s-%s.md",
			msgTime.Format("2006-01-02-150405"),
			sanitizeFilename(chName),
		)
		out = append(out, noteOutput{
			Channel:   chName,
			User:      userName,
			Text:      item.Message.Text,
			Ts:        item.Message.Ts,
			Timestamp: msgTime.UTC().Format(time.RFC3339),
			File:      filename,
		})
	}
	return out
}

func loadState(path string) notesState {
	state := notesState{Captured: make(map[string]string)}
	data, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, &state); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: corrupt state file %s: %v\n", path, err)
	}
	if state.Captured == nil {
		state.Captured = make(map[string]string)
	}
	return state
}

func saveState(path string, state notesState) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling state: %v\n", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving state: %v\n", err)
	}
}

func init() {
	notesCmd.Flags().StringVar(&notesSince, "since", "7d", "How far back to look (1h, 2d, 7d, 30d)")
	notesCmd.Flags().StringVar(&notesEmoji, "emoji", "writing_hand", "Reaction emoji to capture")
	notesCmd.Flags().BoolVar(&notesDryRun, "dry-run", false, "Preview without saving")
	notesCmd.Flags().StringVar(&notesDir, "dir", "", "Notes directory (default ~/Documents/notes/slack/)")
	notesCmd.Flags().StringVar(&notesFormat, "format", "", "Output format: jsonl or md (default jsonl, or SLK_NOTES_FORMAT)")
	rootCmd.AddCommand(notesCmd)
}
