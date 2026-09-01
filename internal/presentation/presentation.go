// Package presentation owns semantic message-presentation modes shared across
// configuration, Slack payload encoding, mutation receipts, and read-back.
package presentation

import (
	"encoding/json"
	"fmt"
)

// Mode controls whether generated Slack section blocks request expansion.
type Mode string

const (
	// SlackManaged leaves section expansion to Slack.
	SlackManaged Mode = "slack-managed"
	// AlwaysExpanded asks Slack to keep generated sections expanded.
	AlwaysExpanded Mode = "always-expanded"
)

// Default returns the compatibility-preserving built-in mode.
func Default() Mode { return SlackManaged }

// Parse resolves one public presentation value.
func Parse(value string) (Mode, bool) {
	mode := Mode(value)
	switch mode {
	case SlackManaged, AlwaysExpanded:
		return mode, true
	default:
		return "", false
	}
}

// Effective applies the built-in default to an omitted mode and rejects unknown
// values before they reach Slack.
func Effective(mode Mode) (Mode, error) {
	if mode == "" {
		return Default(), nil
	}
	if _, known := Parse(string(mode)); !known {
		return "", fmt.Errorf("unknown message presentation %q", mode)
	}
	return mode, nil
}

// DetectBlocks normalizes presentation only when returned Slack block data is
// sufficient and internally consistent. Unknown or mixed custom layouts are not
// guessed.
func DetectBlocks(blocks []json.RawMessage) (Mode, bool) {
	if len(blocks) == 0 {
		return "", false
	}

	var detected Mode
	seenSection := false
	seenRichText := false
	allRichText := true
	for _, raw := range blocks {
		var block struct {
			Type   string `json:"type"`
			Expand *bool  `json:"expand,omitempty"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			return "", false
		}
		if block.Type != "rich_text" {
			allRichText = false
		}
		switch block.Type {
		case "context":
			continue
		case "rich_text":
			seenRichText = true
			continue
		case "section":
			mode := SlackManaged
			if block.Expand != nil && *block.Expand {
				mode = AlwaysExpanded
			}
			if seenSection && detected != mode {
				return "", false
			}
			detected = mode
			seenSection = true
		default:
			return "", false
		}
	}

	if seenSection {
		if seenRichText {
			return "", false
		}
		return detected, true
	}
	if allRichText {
		return SlackManaged, true
	}
	return "", false
}
