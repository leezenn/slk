// Package config loads optional user-level slk settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultReplyPrefix is shown before posted messages unless configuration overrides it.
	DefaultReplyPrefix = ":mechanical_arm: agent assisted response."
	maxPrefixRunes     = 3000
)

// Mutation identifies a Slack-writing command controlled by deny_mutations.
type Mutation string

const (
	MutationReply Mutation = "reply"
	MutationWrite Mutation = "write"
)

var knownMutations = map[Mutation]struct{}{
	MutationReply: {},
	MutationWrite: {},
}

// Settings contains effective slk configuration after defaults are applied.
type Settings struct {
	ReplyPrefix     string
	DeniedMutations []Mutation
}

// Defaults returns effective settings when the optional config file is absent.
func Defaults() Settings {
	return Settings{ReplyPrefix: DefaultReplyPrefix}
}

// ParseMutation resolves a shipped Slack mutation command name.
func ParseMutation(command string) (Mutation, bool) {
	mutation := Mutation(command)
	_, known := knownMutations[mutation]
	return mutation, known
}

// MutationDenied reports whether one shipped Slack mutation is explicitly denied.
func (s Settings) MutationDenied(mutation Mutation) bool {
	for _, denied := range s.DeniedMutations {
		if denied == mutation {
			return true
		}
	}
	return false
}

type fileSettings struct {
	ReplyPrefix     *string  `json:"reply_prefix"`
	DeniedMutations []string `json:"deny_mutations"`
}

// Path returns the stable per-user configuration path.
func Path() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locating home directory: %w", err)
		}
		base = filepath.Join(home, ".config")
	} else if !filepath.IsAbs(base) {
		return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
	}
	return filepath.Join(base, "slk", "config.json"), nil
}

// Load reads the optional user configuration and applies defaults.
func Load() (Settings, error) {
	path, err := Path()
	if err != nil {
		return Settings{}, err
	}
	return LoadFile(path)
}

// LoadFile reads one explicit configuration path. A missing file uses defaults.
func LoadFile(path string) (Settings, error) {
	settings := Defaults()
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var stored fileSettings
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return Settings{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Settings{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	if stored.ReplyPrefix != nil {
		prefix := *stored.ReplyPrefix
		if prefix != "" && strings.TrimSpace(prefix) == "" {
			return Settings{}, fmt.Errorf("parsing %s: reply_prefix must be empty or contain visible text", path)
		}
		if utf8.RuneCountInString(prefix) > maxPrefixRunes {
			return Settings{}, fmt.Errorf("parsing %s: reply_prefix exceeds %d characters", path, maxPrefixRunes)
		}
		settings.ReplyPrefix = prefix
	}

	seen := make(map[Mutation]struct{}, len(stored.DeniedMutations))
	for _, raw := range stored.DeniedMutations {
		mutation, known := ParseMutation(raw)
		if !known {
			return Settings{}, fmt.Errorf("parsing %s: deny_mutations contains unknown command %q", path, raw)
		}
		if _, duplicate := seen[mutation]; duplicate {
			continue
		}
		seen[mutation] = struct{}{}
		settings.DeniedMutations = append(settings.DeniedMutations, mutation)
	}
	return settings, nil
}
