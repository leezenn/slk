// Package config loads and persists user-level slk settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultMessagePrefix is shown before posted messages unless configuration overrides it.
	DefaultMessagePrefix = ":mechanical_arm: agent assisted response."
	maxPrefixRunes       = 3000
)

// Mutation identifies a Slack-writing command controlled by deny_mutations.
type Mutation string

const (
	MutationDelete  Mutation = "delete"
	MutationEdit    Mutation = "edit"
	MutationReplace Mutation = "replace"
	MutationReply   Mutation = "reply"
	MutationWrite   Mutation = "write"
)

var knownMutations = map[Mutation]struct{}{
	MutationDelete:  {},
	MutationEdit:    {},
	MutationReplace: {},
	MutationReply:   {},
	MutationWrite:   {},
}

// Settings contains effective slk configuration after defaults are applied.
type Settings struct {
	Disabled        bool
	MessagePrefix   string
	DeniedMutations []Mutation
}

// Document preserves explicit versus defaulted values for config mutations.
type Document struct {
	Disabled        bool
	MessagePrefix   *string
	DeniedMutations []Mutation
}

// Defaults returns effective settings when the optional config file is absent.
func Defaults() Settings {
	return Settings{MessagePrefix: DefaultMessagePrefix}
}

// Effective applies built-in defaults to a persisted document.
func (d Document) Effective() Settings {
	settings := Defaults()
	settings.Disabled = d.Disabled
	if d.MessagePrefix != nil {
		settings.MessagePrefix = *d.MessagePrefix
	}
	settings.DeniedMutations = append([]Mutation(nil), d.DeniedMutations...)
	return settings
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

// Store owns the stable config path and validated persistence operations.
type Store interface {
	Path() (string, error)
	Load() (Settings, error)
	LoadDocument() (Document, error)
	Save(document Document) error
}

type fileStore struct{}

// NewStore returns the concrete per-user config store.
func NewStore() Store { return fileStore{} }

type fileSettings struct {
	Disabled        bool     `json:"disabled,omitempty"`
	MessagePrefix   *string  `json:"message_prefix,omitempty"`
	DeniedMutations []string `json:"deny_mutations,omitempty"`
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

func (fileStore) Path() (string, error) { return Path() }

// Load reads the optional user configuration and applies defaults.
func Load() (Settings, error) { return fileStore{}.Load() }

func (fileStore) Load() (Settings, error) {
	document, err := fileStore{}.LoadDocument()
	if err != nil {
		return Settings{}, err
	}
	return document.Effective(), nil
}

func (fileStore) LoadDocument() (Document, error) {
	path, err := Path()
	if err != nil {
		return Document{}, err
	}
	return LoadDocumentFile(path)
}

// LoadFile reads one explicit configuration path and applies defaults.
func LoadFile(path string) (Settings, error) {
	document, err := LoadDocumentFile(path)
	if err != nil {
		return Settings{}, err
	}
	return document.Effective(), nil
}

// LoadDocumentFile reads one explicit configuration path. A missing file is empty.
func LoadDocumentFile(path string) (Document, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Document{}, nil
	}
	if err != nil {
		return Document{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var stored fileSettings
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return Document{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Document{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	document := Document{
		Disabled:      stored.Disabled,
		MessagePrefix: stored.MessagePrefix,
	}
	for _, raw := range stored.DeniedMutations {
		mutation, known := ParseMutation(raw)
		if !known {
			return Document{}, fmt.Errorf("parsing %s: deny_mutations contains unknown command %q", path, raw)
		}
		document.DeniedMutations = append(document.DeniedMutations, mutation)
	}
	if err := validateDocument(&document); err != nil {
		return Document{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return document, nil
}

func (fileStore) Save(document Document) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SaveFile(path, document)
}

// SaveFile validates and atomically writes one explicit configuration path.
func SaveFile(path string, document Document) error {
	document.DeniedMutations = append([]Mutation(nil), document.DeniedMutations...)
	if err := validateDocument(&document); err != nil {
		return err
	}

	stored := struct {
		Disabled        bool       `json:"disabled,omitempty"`
		MessagePrefix   *string    `json:"message_prefix,omitempty"`
		DeniedMutations []Mutation `json:"deny_mutations,omitempty"`
	}{
		Disabled:        document.Disabled,
		MessagePrefix:   document.MessagePrefix,
		DeniedMutations: append([]Mutation(nil), document.DeniedMutations...),
	}
	sort.Slice(stored.DeniedMutations, func(i, j int) bool {
		return stored.DeniedMutations[i] < stored.DeniedMutations[j]
	})
	contents, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	contents = append(contents, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protecting temporary config: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("writing temporary config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("syncing temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replacing config: %w", err)
	}
	return nil
}

func validateDocument(document *Document) error {
	if document.MessagePrefix != nil {
		prefix := *document.MessagePrefix
		if prefix != "" && strings.TrimSpace(prefix) == "" {
			return errors.New("message_prefix must be empty or contain visible text")
		}
		if utf8.RuneCountInString(prefix) > maxPrefixRunes {
			return fmt.Errorf("message_prefix exceeds %d characters", maxPrefixRunes)
		}
	}

	seen := make(map[Mutation]struct{}, len(document.DeniedMutations))
	unique := document.DeniedMutations[:0]
	for _, mutation := range document.DeniedMutations {
		if _, known := knownMutations[mutation]; !known {
			return fmt.Errorf("deny_mutations contains unknown command %q", mutation)
		}
		if _, duplicate := seen[mutation]; duplicate {
			continue
		}
		seen[mutation] = struct{}{}
		unique = append(unique, mutation)
	}
	document.DeniedMutations = unique
	return nil
}
