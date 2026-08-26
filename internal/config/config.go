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
	// DefaultReplyPrefix is shown before replies unless configuration overrides it.
	DefaultReplyPrefix = ":mechanical_arm: agent assisted response."
	maxPrefixRunes     = 3000
)

// Settings contains effective slk configuration after defaults are applied.
type Settings struct {
	ReplyPrefix string
}

type fileSettings struct {
	ReplyPrefix *string `json:"reply_prefix"`
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
	settings := Settings{ReplyPrefix: DefaultReplyPrefix}
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

	if stored.ReplyPrefix == nil {
		return settings, nil
	}
	prefix := *stored.ReplyPrefix
	if prefix != "" && strings.TrimSpace(prefix) == "" {
		return Settings{}, fmt.Errorf("parsing %s: reply_prefix must be empty or contain visible text", path)
	}
	if utf8.RuneCountInString(prefix) > maxPrefixRunes {
		return Settings{}, fmt.Errorf("parsing %s: reply_prefix exceeds %d characters", path, maxPrefixRunes)
	}
	settings.ReplyPrefix = prefix
	return settings, nil
}
