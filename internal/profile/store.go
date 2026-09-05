// Package profile owns the authenticated user's general style profile.
package profile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/leezenn/slk/internal/config"
)

const (
	SchemaVersion       = 2
	MinEvidenceLimit    = 6
	MaxEvidenceLimit    = 200
	legacyEvidenceLimit = 100
	maxTextBytes        = 16 * 1024
	maxListItems        = 64
)

var (
	ErrNotFound        = errors.New("style profile is absent")
	ErrDraftOnly       = errors.New("style profile has no approved revision")
	ErrNoDraft         = errors.New("style profile has no draft revision")
	ErrStaleApproval   = errors.New("style profile draft approval is stale")
	ErrAlreadyExists   = errors.New("style profile already exists")
	ErrIncompatible    = errors.New("style profile schema is incompatible")
	ErrInvalidDocument = errors.New("style profile document is invalid")
)

var (
	hexDigestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	credentialPattern = regexp.MustCompile(
		`(?i)(?:xox[a-z0-9]*(?:\.xox[a-z0-9]*)?|xapp|xwfp)-[A-Za-z0-9-]+`,
	)
	privateSlackURLPattern = regexp.MustCompile(`(?i)https?://(?:[a-z0-9-]+\.)*slack\.com(?:[/?#][^\s]*)?`)
)

// Completion records why a bounded evidence window was complete.
type Completion string

const (
	CompletionCapReached      Completion = "cap_reached"
	CompletionSourceExhausted Completion = "source_exhausted"
)

// Coverage is tool-owned evidence metadata. It contains no messages or locators.
type Coverage struct {
	Count      int        `json:"count"`
	Limit      int        `json:"limit,omitempty"`
	WindowFrom time.Time  `json:"window_from"`
	WindowTo   time.Time  `json:"window_to"`
	Completion Completion `json:"completion"`
}

// Content is the allowlisted linguistic profile produced outside slk.
type Content struct {
	LanguagePatterns  []string `json:"language_patterns"`
	Limitations       []string `json:"limitations"`
	SyntheticExamples []string `json:"synthetic_examples,omitempty"`
}

// CreateInput carries software-derived coverage beside semantic model output.
type CreateInput struct {
	Coverage Coverage `json:"coverage"`
	Profile  Content  `json:"profile"`
}

// Revision is one immutable reviewed profile value.
type Revision struct {
	Digest     string     `json:"digest"`
	CreatedAt  time.Time  `json:"created_at"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	Coverage   Coverage   `json:"coverage"`
	Content    Content    `json:"content"`
}

// Document stores at most one approved revision and one current draft.
type Document struct {
	SchemaVersion int       `json:"schema_version"`
	Approved      *Revision `json:"approved,omitempty"`
	Draft         *Revision `json:"draft,omitempty"`
}

// State is the current general-profile lifecycle state.
type State string

const (
	StateAbsent            State = "absent"
	StateDraft             State = "draft"
	StateApproved          State = "approved"
	StateApprovedWithDraft State = "approved_with_draft"
	StateIncompatible      State = "incompatible"
)

// Status is the bounded state shown by the style command.
type Status struct {
	State          State  `json:"state"`
	ApprovedDigest string `json:"approved_digest,omitempty"`
	DraftDigest    string `json:"draft_digest,omitempty"`
	Count          int    `json:"evidence_count,omitempty"`
}

// Store owns profile lifecycle transitions for the authenticated identity.
type Store interface {
	Status(config.Identity) (Status, error)
	Use(config.Identity) (Revision, error)
	Review(config.Identity) (Revision, error)
	CreateDraft(config.Identity, Content, Coverage, time.Time) (Revision, error)
	Adjust(config.Identity, Content, time.Time) (Revision, error)
	Approve(config.Identity, string, time.Time) (Revision, error)
}

type fileStore struct{}

// NewStore creates the local profile store.
func NewStore() Store { return fileStore{} }

func (fileStore) Path(identity config.Identity) (string, error) {
	identityRef, err := identity.Namespace()
	if err != nil {
		return "", err
	}
	configPath, err := config.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), "profiles", identityRef, "general.json"), nil
}

func (f fileStore) Status(identity config.Identity) (Status, error) {
	path, err := f.Path(identity)
	if err != nil {
		return Status{}, err
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Status{State: StateAbsent}, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("reading style profile: %w", err)
	}
	document, err := decodeDocument(contents)
	if errors.Is(err, ErrIncompatible) {
		return Status{State: StateIncompatible}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return profileStatus(document), nil
}

func (f fileStore) Use(identity config.Identity) (Revision, error) {
	document, err := f.load(identity)
	if err != nil {
		return Revision{}, err
	}
	if document.Approved == nil {
		if document.Draft != nil {
			return Revision{}, ErrDraftOnly
		}
		return Revision{}, ErrNotFound
	}
	return cloneRevision(*document.Approved), nil
}

func (f fileStore) Review(identity config.Identity) (Revision, error) {
	document, err := f.load(identity)
	if err != nil {
		return Revision{}, err
	}
	if document.Draft == nil {
		return Revision{}, ErrNoDraft
	}
	return cloneRevision(*document.Draft), nil
}

func (f fileStore) CreateDraft(identity config.Identity, content Content, coverage Coverage, now time.Time) (Revision, error) {
	path, err := f.Path(identity)
	if err != nil {
		return Revision{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return Revision{}, ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return Revision{}, fmt.Errorf("inspecting style profile: %w", err)
	}
	revision, err := newRevision(content, coverage, now)
	if err != nil {
		return Revision{}, err
	}
	document := newDocument()
	document.Draft = &revision
	if err := f.save(path, document); err != nil {
		return Revision{}, err
	}
	return cloneRevision(revision), nil
}

func (f fileStore) Adjust(identity config.Identity, content Content, now time.Time) (Revision, error) {
	document, err := f.load(identity)
	if err != nil {
		return Revision{}, err
	}
	coverage := Coverage{}
	switch {
	case document.Draft != nil:
		coverage = document.Draft.Coverage
	case document.Approved != nil:
		coverage = document.Approved.Coverage
	default:
		return Revision{}, ErrNotFound
	}
	if coverage.Limit == 0 {
		coverage.Limit = legacyEvidenceLimit
	}
	revision, err := newRevision(content, coverage, now)
	if err != nil {
		return Revision{}, err
	}
	document.Draft = &revision
	path, err := f.Path(identity)
	if err != nil {
		return Revision{}, err
	}
	if err := f.save(path, document); err != nil {
		return Revision{}, err
	}
	return cloneRevision(revision), nil
}

func (f fileStore) Approve(identity config.Identity, expectedDigest string, now time.Time) (Revision, error) {
	if !hexDigestPattern.MatchString(expectedDigest) {
		return Revision{}, ErrStaleApproval
	}
	document, err := f.load(identity)
	if err != nil {
		return Revision{}, err
	}
	if document.Draft == nil {
		return Revision{}, ErrNoDraft
	}
	if document.Draft.Digest != expectedDigest {
		return Revision{}, ErrStaleApproval
	}
	approved := cloneRevision(*document.Draft)
	approvedAt := now.UTC()
	if approvedAt.IsZero() {
		return Revision{}, fmt.Errorf("%w: approval timestamp is required", ErrInvalidDocument)
	}
	approved.ApprovedAt = &approvedAt
	document.Approved = &approved
	document.Draft = nil
	path, err := f.Path(identity)
	if err != nil {
		return Revision{}, err
	}
	if err := f.save(path, document); err != nil {
		return Revision{}, err
	}
	return cloneRevision(approved), nil
}

func (f fileStore) load(identity config.Identity) (Document, error) {
	path, err := f.Path(identity)
	if err != nil {
		return Document{}, err
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("reading style profile: %w", err)
	}
	return decodeDocument(contents)
}

func (fileStore) save(path string, document Document) error {
	contents, err := encodeDocument(document)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("creating style profile directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protecting style profile directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".profile-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary style profile: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protecting temporary style profile: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("writing temporary style profile: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("syncing temporary style profile: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing temporary style profile: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replacing style profile: %w", err)
	}
	return nil
}

// DecodeContent reads one strict semantic object and rejects unknown fields.
func DecodeContent(reader io.Reader) (Content, error) {
	var content Content
	if err := decodeStrict(reader, "semantic profile", &content); err != nil {
		return Content{}, err
	}
	if err := validateContent(content); err != nil {
		return Content{}, err
	}
	return cloneContent(content), nil
}

// DecodeCreateInput reads one coverage-and-profile envelope for draft creation.
func DecodeCreateInput(reader io.Reader) (CreateInput, error) {
	var input CreateInput
	if err := decodeStrict(reader, "style creation input", &input); err != nil {
		return CreateInput{}, err
	}
	if err := validateNewCoverage(input.Coverage); err != nil {
		return CreateInput{}, err
	}
	if err := validateContent(input.Profile); err != nil {
		return CreateInput{}, err
	}
	input.Profile = cloneContent(input.Profile)
	return input, nil
}

func decodeStrict(reader io.Reader, label string, target interface{}) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decoding %s: %w", label, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("decoding %s: %w", label, err)
	}
	return nil
}

func newDocument() Document { return Document{SchemaVersion: SchemaVersion} }

func newRevision(content Content, coverage Coverage, now time.Time) (Revision, error) {
	if now.IsZero() {
		return Revision{}, fmt.Errorf("%w: creation timestamp is required", ErrInvalidDocument)
	}
	if err := validateContent(content); err != nil {
		return Revision{}, err
	}
	if err := validateNewCoverage(coverage); err != nil {
		return Revision{}, err
	}
	revision := Revision{
		CreatedAt: now.UTC(),
		Coverage:  coverage,
		Content:   cloneContent(content),
	}
	digest, err := digestRevision(revision)
	if err != nil {
		return Revision{}, err
	}
	revision.Digest = digest
	return revision, nil
}

func digestRevision(revision Revision) (string, error) {
	value := struct {
		CreatedAt time.Time `json:"created_at"`
		Coverage  Coverage  `json:"coverage"`
		Content   Content   `json:"content"`
	}{revision.CreatedAt, revision.Coverage, revision.Content}
	contents, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encoding style profile digest: %w", err)
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:]), nil
}

func encodeDocument(document Document) ([]byte, error) {
	if err := validateDocument(document); err != nil {
		return nil, err
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding style profile: %w", err)
	}
	return append(contents, '\n'), nil
}

func decodeDocument(contents []byte) (Document, error) {
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(contents, &envelope); err != nil {
		return Document{}, invalidJSON(err)
	}
	if envelope.SchemaVersion != SchemaVersion {
		return Document{}, fmt.Errorf("%w: found version %d; supported version is %d", ErrIncompatible, envelope.SchemaVersion, SchemaVersion)
	}

	var document Document
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Document{}, invalidJSON(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Document{}, invalidJSON(err)
	}
	if err := validateDocument(document); err != nil {
		return Document{}, err
	}
	return cloneDocument(document), nil
}

func invalidJSON(err error) error {
	return fmt.Errorf("%w: decoding JSON: %v", ErrInvalidDocument, err)
}

func validateDocument(document Document) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: found version %d; supported version is %d", ErrIncompatible, document.SchemaVersion, SchemaVersion)
	}
	if document.Approved == nil && document.Draft == nil {
		return fmt.Errorf("%w: document contains no revision", ErrInvalidDocument)
	}
	if document.Approved != nil {
		if err := validateRevision(*document.Approved, true); err != nil {
			return err
		}
	}
	if document.Draft != nil {
		if err := validateRevision(*document.Draft, false); err != nil {
			return err
		}
	}
	if document.Approved != nil && document.Draft != nil && document.Approved.Digest == document.Draft.Digest {
		return fmt.Errorf("%w: approved and draft revisions must differ", ErrInvalidDocument)
	}
	return nil
}

func validateRevision(revision Revision, approved bool) error {
	if !hexDigestPattern.MatchString(revision.Digest) {
		return fmt.Errorf("%w: invalid revision digest", ErrInvalidDocument)
	}
	if revision.CreatedAt.IsZero() {
		return fmt.Errorf("%w: revision creation timestamp is required", ErrInvalidDocument)
	}
	if approved && revision.ApprovedAt == nil {
		return fmt.Errorf("%w: approved revision timestamp is required", ErrInvalidDocument)
	}
	if approved && revision.ApprovedAt.Before(revision.CreatedAt) {
		return fmt.Errorf("%w: approval timestamp precedes revision creation", ErrInvalidDocument)
	}
	if !approved && revision.ApprovedAt != nil {
		return fmt.Errorf("%w: draft revision cannot have approval timestamp", ErrInvalidDocument)
	}
	if err := validateCoverage(revision.Coverage); err != nil {
		return err
	}
	if err := validateContent(revision.Content); err != nil {
		return err
	}
	digest, err := digestRevision(revision)
	if err != nil {
		return err
	}
	if digest != revision.Digest {
		return fmt.Errorf("%w: revision digest mismatch", ErrInvalidDocument)
	}
	return nil
}

func validateNewCoverage(coverage Coverage) error {
	if coverage.Limit == 0 {
		return fmt.Errorf("%w: evidence limit is required for a new revision", ErrInvalidDocument)
	}
	return validateCoverage(coverage)
}

func validateCoverage(coverage Coverage) error {
	limit := coverage.Limit
	if limit == 0 {
		limit = legacyEvidenceLimit
	}
	if limit < MinEvidenceLimit || limit > MaxEvidenceLimit {
		return fmt.Errorf("%w: evidence limit must be from %d through %d", ErrInvalidDocument, MinEvidenceLimit, MaxEvidenceLimit)
	}
	if coverage.Count < MinEvidenceLimit || coverage.Count > limit {
		return fmt.Errorf("%w: evidence count must be from %d through its limit of %d", ErrInvalidDocument, MinEvidenceLimit, limit)
	}
	if coverage.WindowFrom.IsZero() || coverage.WindowTo.IsZero() || coverage.WindowFrom.After(coverage.WindowTo) {
		return fmt.Errorf("%w: evidence window is invalid", ErrInvalidDocument)
	}
	switch coverage.Completion {
	case CompletionCapReached:
		if coverage.Count != limit {
			return fmt.Errorf("%w: cap_reached requires exactly the configured limit of %d qualifying messages", ErrInvalidDocument, limit)
		}
	case CompletionSourceExhausted:
	default:
		return fmt.Errorf("%w: evidence completion must be cap_reached or source_exhausted", ErrInvalidDocument)
	}
	return nil
}

func validateContent(content Content) error {
	if len(content.LanguagePatterns) == 0 || len(content.LanguagePatterns) > maxListItems {
		return fmt.Errorf("%w: language_patterns must contain 1 through %d items", ErrInvalidDocument, maxListItems)
	}
	for _, value := range content.LanguagePatterns {
		if err := validateText("language_patterns", value); err != nil {
			return err
		}
	}
	if content.Limitations == nil {
		return fmt.Errorf("%w: limitations must be an array", ErrInvalidDocument)
	}
	if len(content.Limitations) > maxListItems {
		return fmt.Errorf("%w: limitations may contain at most %d items", ErrInvalidDocument, maxListItems)
	}
	for _, value := range content.Limitations {
		if err := validateText("limitations", value); err != nil {
			return err
		}
	}
	if len(content.SyntheticExamples) > 8 {
		return fmt.Errorf("%w: synthetic_examples may contain at most 8 items", ErrInvalidDocument)
	}
	for _, value := range content.SyntheticExamples {
		if err := validateText("synthetic_examples", value); err != nil {
			return err
		}
	}
	return nil
}

func validateText(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) || len(value) > maxTextBytes {
		return fmt.Errorf("%w: %s must contain bounded visible UTF-8 text", ErrInvalidDocument, name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: %s must not contain control characters", ErrInvalidDocument, name)
		}
	}
	if credentialPattern.MatchString(value) || privateSlackURLPattern.MatchString(value) {
		return fmt.Errorf("%w: %s contains forbidden private or credential material", ErrInvalidDocument, name)
	}
	return nil
}

func profileStatus(document Document) Status {
	status := Status{}
	switch {
	case document.Approved != nil && document.Draft != nil:
		status.State = StateApprovedWithDraft
		status.ApprovedDigest = document.Approved.Digest
		status.DraftDigest = document.Draft.Digest
		status.Count = document.Approved.Coverage.Count
	case document.Approved != nil:
		status.State = StateApproved
		status.ApprovedDigest = document.Approved.Digest
		status.Count = document.Approved.Coverage.Count
	case document.Draft != nil:
		status.State = StateDraft
		status.DraftDigest = document.Draft.Digest
		status.Count = document.Draft.Coverage.Count
	}
	return status
}

func cloneContent(content Content) Content {
	content.LanguagePatterns = slices.Clone(content.LanguagePatterns)
	content.Limitations = slices.Clone(content.Limitations)
	content.SyntheticExamples = slices.Clone(content.SyntheticExamples)
	return content
}

func cloneRevision(revision Revision) Revision {
	revision.Content = cloneContent(revision.Content)
	if revision.ApprovedAt != nil {
		approvedAt := *revision.ApprovedAt
		revision.ApprovedAt = &approvedAt
	}
	return revision
}

func cloneDocument(document Document) Document {
	if document.Approved != nil {
		approved := cloneRevision(*document.Approved)
		document.Approved = &approved
	}
	if document.Draft != nil {
		draft := cloneRevision(*document.Draft)
		document.Draft = &draft
	}
	return document
}
