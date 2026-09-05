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

	"github.com/leezenn/slk/internal/presentation"
	"github.com/leezenn/slk/internal/textformat"
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
	Disabled            bool
	MessagePrefix       string
	MessagePresentation presentation.Mode
	DeniedMutations     []Mutation
	Formatting          []textformat.Module
}

// Preferences contains settings owned by one authenticated Slack identity.
type Preferences struct {
	MessagePrefix       *string             `json:"message_prefix,omitempty"`
	MessagePresentation *presentation.Mode  `json:"message_presentation,omitempty"`
	Formatting          []textformat.Module `json:"formatting,omitempty"`
}

// Document is the complete configuration persisted in one file.
type Document struct {
	Disabled        bool                   `json:"disabled,omitempty"`
	DeniedMutations []Mutation             `json:"deny_mutations,omitempty"`
	Identities      map[string]Preferences `json:"identities,omitempty"`

	// These top-level preference fields read the released flat format. BindIdentity
	// moves them into the first validated identity entry.
	LegacyMessagePrefix       *string             `json:"message_prefix,omitempty"`
	LegacyMessagePresentation *presentation.Mode  `json:"message_presentation,omitempty"`
	LegacyFormatting          []textformat.Module `json:"formatting,omitempty"`
}

// Store owns the one stable configuration file.
type Store interface {
	Path() (string, error)
	Load() (Document, error)
	Save(Document) error
}

type fileStore struct{}

// NewStore returns the concrete per-user config store.
func NewStore() Store { return fileStore{} }

// Defaults returns effective settings when configuration is absent.
func Defaults() Settings {
	return Settings{
		MessagePrefix:       DefaultMessagePrefix,
		MessagePresentation: presentation.Default(),
	}
}

// Effective applies machine policy and built-in preference defaults.
func (d Document) Effective() Settings {
	settings := Defaults()
	settings.Disabled = d.Disabled
	settings.DeniedMutations = append([]Mutation(nil), d.DeniedMutations...)
	return settings
}

// Effective applies built-in defaults to identity preferences.
func (p Preferences) Effective() Settings {
	settings := Defaults()
	if p.MessagePrefix != nil {
		settings.MessagePrefix = *p.MessagePrefix
	}
	if p.MessagePresentation != nil {
		settings.MessagePresentation = *p.MessagePresentation
	}
	settings.Formatting = append([]textformat.Module(nil), p.Formatting...)
	return settings
}

// Merge combines machine policy with one identity's preferences.
func Merge(machine Document, identity Preferences) Settings {
	settings := machine.Effective()
	preferences := identity.Effective()
	settings.MessagePrefix = preferences.MessagePrefix
	settings.MessagePresentation = preferences.MessagePresentation
	settings.Formatting = preferences.Formatting
	return settings
}

// Preferences returns one identity's stored preferences, or defaults when absent.
func (d Document) Preferences(identity Identity) (Preferences, error) {
	namespace, err := identity.Namespace()
	if err != nil {
		return Preferences{}, err
	}
	return clonePreferences(d.Identities[namespace]), nil
}

// BindIdentity assigns released flat preferences to the first validated identity.
// The caller saves the document when changed is true.
func (d *Document) BindIdentity(identity Identity) (preferences Preferences, changed bool, err error) {
	namespace, err := identity.Namespace()
	if err != nil {
		return Preferences{}, false, err
	}
	if preferences, ok := d.Identities[namespace]; ok {
		return clonePreferences(preferences), false, nil
	}
	preferences = d.legacyPreferences()
	if !hasPreferences(preferences) {
		return Preferences{}, false, nil
	}
	if len(d.Identities) != 0 {
		return Preferences{}, false, errors.New("legacy preferences cannot be assigned because identity preferences already exist")
	}
	d.clearLegacyPreferences()
	if d.Identities == nil {
		d.Identities = make(map[string]Preferences)
	}
	d.Identities[namespace] = clonePreferences(preferences)
	return preferences, true, nil
}

// SetPreferences replaces one identity's preferences in the aggregate document.
func (d *Document) SetPreferences(identity Identity, preferences Preferences) error {
	namespace, err := identity.Namespace()
	if err != nil {
		return err
	}
	preferences = clonePreferences(preferences)
	if err := validatePreferences(&preferences); err != nil {
		return err
	}
	if !hasPreferences(preferences) {
		delete(d.Identities, namespace)
		return nil
	}
	if d.Identities == nil {
		d.Identities = make(map[string]Preferences)
	}
	d.Identities[namespace] = preferences
	return nil
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

// FormattingEnabled reports whether one shipped formatting module is enabled.
func (s Settings) FormattingEnabled(module textformat.Module) bool {
	for _, enabled := range s.Formatting {
		if enabled == module {
			return true
		}
	}
	return false
}

func configBase() (string, error) {
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
	return filepath.Join(base, "slk"), nil
}

// Path returns the stable configuration path.
func Path() (string, error) { return fileStore{}.Path() }

func (fileStore) Path() (string, error) {
	base, err := configBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "config.json"), nil
}

func (f fileStore) Load() (Document, error) {
	path, err := f.Path()
	if err != nil {
		return Document{}, err
	}
	return LoadFile(path)
}

// LoadFile reads one complete configuration document. A missing file is empty.
func LoadFile(path string) (Document, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Document{}, nil
	}
	if err != nil {
		return Document{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var document Document
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Document{}, configParseError(path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Document{}, configParseError(path, err)
	}

	if err := validateDocument(&document); err != nil {
		return Document{}, configParseError(path, err)
	}
	return document, nil
}

func configParseError(path string, err error) error {
	return fmt.Errorf("parsing %s: %w", path, err)
}

func (f fileStore) Save(document Document) error {
	path, err := f.Path()
	if err != nil {
		return err
	}
	return SaveFile(path, document)
}

// SaveFile validates and atomically replaces one complete configuration file.
func SaveFile(path string, document Document) error {
	document = cloneDocument(document)
	if err := validateDocument(&document); err != nil {
		return err
	}

	sort.Slice(document.DeniedMutations, func(i, j int) bool { return document.DeniedMutations[i] < document.DeniedMutations[j] })
	sort.Slice(document.LegacyFormatting, func(i, j int) bool { return document.LegacyFormatting[i] < document.LegacyFormatting[j] })
	for namespace, preferences := range document.Identities {
		sort.Slice(preferences.Formatting, func(i, j int) bool { return preferences.Formatting[i] < preferences.Formatting[j] })
		document.Identities[namespace] = preferences
	}

	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	contents = append(contents, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := protectDirectory(directory); err != nil {
		return err
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
	document.DeniedMutations = unique(document.DeniedMutations)
	for _, mutation := range document.DeniedMutations {
		if _, known := knownMutations[mutation]; !known {
			return fmt.Errorf("deny_mutations contains unknown command %q", mutation)
		}
	}
	legacy := document.legacyPreferences()
	if hasPreferences(legacy) {
		if err := validatePreferences(&legacy); err != nil {
			return err
		}
		document.setLegacyPreferences(legacy)
		if len(document.Identities) != 0 {
			return errors.New("legacy and identity preferences cannot coexist")
		}
	}
	for namespace, preferences := range document.Identities {
		if !validNamespace(namespace) {
			return errors.New("identities contains an invalid opaque identity key")
		}
		preferences = clonePreferences(preferences)
		if err := validatePreferences(&preferences); err != nil {
			return fmt.Errorf("identity %s: %w", namespace, err)
		}
		if hasPreferences(preferences) {
			document.Identities[namespace] = preferences
		} else {
			delete(document.Identities, namespace)
		}
	}
	return nil
}

func validatePreferences(preferences *Preferences) error {
	if preferences.MessagePresentation != nil {
		if _, known := presentation.Parse(string(*preferences.MessagePresentation)); !known {
			return fmt.Errorf("message_presentation must be %q or %q", presentation.SlackManaged, presentation.AlwaysExpanded)
		}
	}
	if preferences.MessagePrefix != nil {
		prefix := *preferences.MessagePrefix
		if prefix != "" && strings.TrimSpace(prefix) == "" {
			return errors.New("message_prefix must be empty or contain visible text")
		}
		if utf8.RuneCountInString(prefix) > maxPrefixRunes {
			return fmt.Errorf("message_prefix exceeds %d characters", maxPrefixRunes)
		}
	}

	preferences.Formatting = unique(preferences.Formatting)
	for _, module := range preferences.Formatting {
		if _, known := textformat.ParseModule(string(module)); !known {
			return fmt.Errorf("formatting contains unknown module %q", module)
		}
	}
	return nil
}

func hasPreferences(preferences Preferences) bool {
	return preferences.MessagePrefix != nil || preferences.MessagePresentation != nil || len(preferences.Formatting) != 0
}

func unique[T comparable](values []T) []T {
	seen := make(map[T]struct{}, len(values))
	result := make([]T, 0, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func clonePreferences(preferences Preferences) Preferences {
	if preferences.MessagePrefix != nil {
		value := *preferences.MessagePrefix
		preferences.MessagePrefix = &value
	}
	if preferences.MessagePresentation != nil {
		value := *preferences.MessagePresentation
		preferences.MessagePresentation = &value
	}
	preferences.Formatting = append([]textformat.Module(nil), preferences.Formatting...)
	return preferences
}

func cloneIdentities(identities map[string]Preferences) map[string]Preferences {
	if len(identities) == 0 {
		return nil
	}
	cloned := make(map[string]Preferences, len(identities))
	for namespace, preferences := range identities {
		cloned[namespace] = clonePreferences(preferences)
	}
	return cloned
}

func (d Document) legacyPreferences() Preferences {
	return clonePreferences(Preferences{
		MessagePrefix:       d.LegacyMessagePrefix,
		MessagePresentation: d.LegacyMessagePresentation,
		Formatting:          d.LegacyFormatting,
	})
}

func (d *Document) setLegacyPreferences(preferences Preferences) {
	preferences = clonePreferences(preferences)
	d.LegacyMessagePrefix = preferences.MessagePrefix
	d.LegacyMessagePresentation = preferences.MessagePresentation
	d.LegacyFormatting = preferences.Formatting
}

func (d *Document) clearLegacyPreferences() {
	d.LegacyMessagePrefix = nil
	d.LegacyMessagePresentation = nil
	d.LegacyFormatting = nil
}

func cloneDocument(document Document) Document {
	document.DeniedMutations = append([]Mutation(nil), document.DeniedMutations...)
	document.Identities = cloneIdentities(document.Identities)
	document.setLegacyPreferences(document.legacyPreferences())
	return document
}

func protectDirectory(path string) error {
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protecting configuration directory: %w", err)
	}
	return nil
}
