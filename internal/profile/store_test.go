package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leezenn/slk/internal/config"
)

func testContent(pattern string) Content {
	return Content{
		LanguagePatterns:  []string{pattern, "Uses concise factual statements"},
		Limitations:       []string{"Synthetic fixture covers one stable scope only"},
		SyntheticExamples: []string{"Synthetic example: The change is ready."},
	}
}

func testCoverage() Coverage {
	return Coverage{
		Count:      12,
		Limit:      100,
		WindowFrom: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		WindowTo:   time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
		Completion: CompletionSourceExhausted,
	}
}

func testIdentity(t *testing.T, teamID, userID string) config.Identity {
	t.Helper()
	identity, err := config.NewIdentity(teamID, userID)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func testStore(t *testing.T) (fileStore, config.Identity) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return fileStore{}, testIdentity(t, "T111", "U111")
}

func createApproved(t *testing.T, store fileStore, identity config.Identity, content Content) Revision {
	t.Helper()
	draft, err := store.CreateDraft(identity, content, testCoverage(), time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := store.Approve(identity, draft.Digest, time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return approved
}

func TestProfileStoreRejectsUnknownAndIncompatibleSchema(t *testing.T) {
	store, identity := testStore(t)
	if _, err := store.CreateDraft(identity, testContent("strict schema"), testCoverage(), time.Now()); err != nil {
		t.Fatal(err)
	}
	path, _ := store.Path(identity)
	original, _ := os.ReadFile(path)

	unknown := bytes.Replace(original, []byte(`"schema_version": 2,`), []byte(`"schema_version": 2, "raw_prompt": "PROMPT_SENTINEL",`), 1)
	if err := os.WriteFile(path, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Review(identity); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("unknown-field review error = %v", err)
	}

	future := bytes.Replace(original, []byte(`"schema_version": 2,`), []byte(`"schema_version": 99, "future_field": true,`), 1)
	if err := os.WriteFile(path, future, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Use(identity); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("future-schema use error = %v", err)
	}
	status, err := store.Status(identity)
	if err != nil || status.State != StateIncompatible {
		t.Fatalf("future-schema status = %#v, %v", status, err)
	}
}

func TestRequiredReviewLifecycleUsesOneExactDigest(t *testing.T) {
	store, identity := testStore(t)
	first, err := store.CreateDraft(identity, testContent("first draft"), testCoverage(), time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Use(identity); !errors.Is(err, ErrDraftOnly) {
		t.Fatalf("draft use error = %v", err)
	}
	reviewed, err := store.Review(identity)
	if err != nil || reviewed.Digest != first.Digest {
		t.Fatalf("reviewed draft = %#v, %v", reviewed, err)
	}

	second, err := store.Adjust(identity, testContent("replacement draft"), time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC))
	if err != nil || second.Digest == first.Digest {
		t.Fatalf("replacement draft = %#v, %v", second, err)
	}
	path, _ := store.Path(identity)
	beforeStale, _ := os.ReadFile(path)
	if _, err := store.Approve(identity, first.Digest, time.Now()); !errors.Is(err, ErrStaleApproval) {
		t.Fatalf("stale approval error = %v", err)
	}
	afterStale, _ := os.ReadFile(path)
	if !bytes.Equal(beforeStale, afterStale) {
		t.Fatal("stale approval changed the profile")
	}

	approved, err := store.Approve(identity, second.Digest, time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	if err != nil || approved.ApprovedAt == nil {
		t.Fatalf("approved revision = %#v, %v", approved, err)
	}
	pending, err := store.Adjust(identity, testContent("pending adjustment"), time.Date(2026, 8, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	used, err := store.Use(identity)
	if err != nil || used.Digest != approved.Digest || used.Digest == pending.Digest {
		t.Fatalf("adjustment replaced approved revision: %#v %#v %v", used, pending, err)
	}
	status, err := store.Status(identity)
	if err != nil || status.State != StateApprovedWithDraft || status.DraftDigest != pending.Digest {
		t.Fatalf("status = %#v, %v", status, err)
	}
}

func TestProfilePathIsIdentityScopedAndProtected(t *testing.T) {
	store, first := testStore(t)
	second := testIdentity(t, "T222", "U222")
	createApproved(t, store, first, testContent("first general"))
	if _, err := store.Use(second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second identity selected first profile: %v", err)
	}

	firstPath, _ := store.Path(first)
	secondPath, _ := store.Path(second)
	if firstPath == secondPath || filepath.Base(firstPath) != "general.json" {
		t.Fatalf("profile paths = %q %q", firstPath, secondPath)
	}
	for _, id := range []string{first.TeamID, first.UserID, second.TeamID, second.UserID} {
		if strings.Contains(firstPath, id) || strings.Contains(secondPath, id) {
			t.Fatalf("profile path exposed %q", id)
		}
	}
	for _, target := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Dir(firstPath), 0o700}, {firstPath, 0o600}} {
		info, err := os.Stat(target.path)
		if err != nil || info.Mode().Perm() != target.mode {
			t.Fatalf("mode %s = %v, %v", target.path, info, err)
		}
	}

	contents, _ := os.ReadFile(firstPath)
	for _, forbidden := range []string{first.TeamID, first.UserID, "https://workspace.slack.com/archives/", "xoxp-", `"scope"`} {
		if bytes.Contains(contents, []byte(forbidden)) {
			t.Fatalf("profile exposed %q: %s", forbidden, contents)
		}
	}
}

func TestContentValidationReportsFieldsInSchemaOrder(t *testing.T) {
	_, err := DecodeContent(strings.NewReader(`{}`))
	if err == nil || !strings.Contains(err.Error(), "language_patterns") {
		t.Fatalf("empty content error = %v", err)
	}
}

func TestDecodeCreateInputRequiresStrictCoverageAndSemanticProfile(t *testing.T) {
	valid, err := json.Marshal(CreateInput{Coverage: testCoverage(), Profile: testContent("creation")})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCreateInput(bytes.NewReader(valid))
	if err != nil || decoded.Coverage.Count != 12 || decoded.Profile.LanguagePatterns[0] != "creation" {
		t.Fatalf("DecodeCreateInput() = %#v, %v", decoded, err)
	}

	for name, raw := range map[string]string{
		"unknown top-level field": strings.TrimSuffix(string(valid), "}") + `,"raw_evidence":["private"]}`,
		"unknown profile field":   strings.Replace(string(valid), `"profile":{`, `"profile":{"source_messages":["private"],`, 1),
		"missing selected limit":  strings.Replace(string(valid), `"limit":100,`, "", 1),
		"invalid coverage":        strings.Replace(string(valid), `"count":12`, `"count":5`, 1),
		"multiple values":         string(valid) + ` {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCreateInput(strings.NewReader(raw)); err == nil {
				t.Fatal("invalid creation input passed validation")
			}
		})
	}
}

func TestCreateDraftReportsExistingProfile(t *testing.T) {
	store, identity := testStore(t)
	if _, err := store.CreateDraft(identity, testContent("first"), testCoverage(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDraft(identity, testContent("second"), testCoverage(), time.Now()); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second CreateDraft() error = %v", err)
	}
}

func TestProfileValidationRejectsPrivateMaterialWithoutChangingApproved(t *testing.T) {
	store, identity := testStore(t)
	approved := createApproved(t, store, identity, testContent("privacy baseline"))
	path, _ := store.Path(identity)
	before, _ := os.ReadFile(path)

	for name, raw := range map[string]string{
		"unknown source field": `{"language_patterns":["ok"],"limitations":[],"source_messages":["EVIDENCE_SENTINEL"]}`,
		"private link":         `{"language_patterns":["https://workspace.slack.com/archives/C1/p123"],"limitations":[]}`,
		"credential":           `{"language_patterns":["xoxp-SECRET-SENTINEL"],"limitations":[]}`,
		"terminal control":     `{"language_patterns":["safe\u001b[31munsafe"],"limitations":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeContent(strings.NewReader(raw)); err == nil {
				t.Fatal("invalid profile content passed validation")
			}
		})
	}
	if _, err := store.Adjust(identity, testContent("xoxp-SECRET-SENTINEL"), time.Now()); err == nil {
		t.Fatal("invalid adjustment succeeded")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed validation changed the profile")
	}
	used, err := store.Use(identity)
	if err != nil || used.Digest != approved.Digest {
		t.Fatalf("approved profile changed: %#v, %v", used, err)
	}
}

func TestProfileStoreReadsLegacyCoverageAndNormalizesOnlyANewRevision(t *testing.T) {
	store, identity := testStore(t)
	coverage := testCoverage()
	coverage.Limit = 0
	createdAt := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	legacy := Revision{CreatedAt: createdAt, Coverage: coverage, Content: testContent("legacy draft")}
	digest, err := digestRevision(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Digest = digest
	path, err := store.Path(identity)
	if err != nil {
		t.Fatal(err)
	}
	document := newDocument()
	document.Draft = &legacy
	if err := store.save(path, document); err != nil {
		t.Fatal(err)
	}

	reviewed, err := store.Review(identity)
	if err != nil || reviewed.Digest != digest || reviewed.Coverage.Limit != 0 {
		t.Fatalf("legacy review = %#v, %v", reviewed, err)
	}
	adjusted, err := store.Adjust(identity, testContent("adjusted legacy draft"), createdAt.Add(time.Hour))
	if err != nil || adjusted.Coverage.Limit != legacyEvidenceLimit || adjusted.Digest == digest {
		t.Fatalf("legacy adjustment = %#v, %v", adjusted, err)
	}
}

func TestProfileCoverageBounds(t *testing.T) {
	for _, test := range []struct {
		count      int
		limit      int
		completion Completion
		valid      bool
	}{
		{5, 100, CompletionSourceExhausted, false},
		{6, 6, CompletionCapReached, true},
		{6, 100, CompletionSourceExhausted, true},
		{99, 100, CompletionCapReached, false},
		{100, 0, CompletionCapReached, false},
		{100, 200, CompletionCapReached, false},
		{150, 200, CompletionSourceExhausted, true},
		{200, 200, CompletionCapReached, true},
		{201, 200, CompletionSourceExhausted, false},
		{100, 201, CompletionSourceExhausted, false},
	} {
		t.Run(fmt.Sprintf("%d_of_%d_%s", test.count, test.limit, test.completion), func(t *testing.T) {
			store, identity := testStore(t)
			coverage := testCoverage()
			coverage.Count = test.count
			coverage.Limit = test.limit
			coverage.Completion = test.completion
			_, err := store.CreateDraft(identity, testContent("coverage"), coverage, time.Now())
			if (err == nil) != test.valid {
				t.Fatalf("CreateDraft() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestPersistedRevisionDigestMatchesReviewedValue(t *testing.T) {
	store, identity := testStore(t)
	draft, err := store.CreateDraft(identity, testContent("digest"), testCoverage(), time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	path, _ := store.Path(identity)
	contents, _ := os.ReadFile(path)
	var document Document
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	if document.Draft == nil || document.Draft.Digest != draft.Digest {
		t.Fatalf("persisted draft = %#v", document.Draft)
	}
}

func TestSparseLinguisticContentContract(t *testing.T) {
	valid := `{"language_patterns":["Uses lower-case sentence openings in brief updates"],"limitations":[]}`
	content, err := DecodeContent(strings.NewReader(valid))
	if err != nil || content.Limitations == nil || len(content.Limitations) != 0 {
		t.Fatalf("valid sparse content = %#v, %v", content, err)
	}
	if _, err := DecodeContent(strings.NewReader(`{"language_patterns":["x"],"limitations":[],"synthetic_examples":["Illustration: short sentence."]}`)); err != nil {
		t.Fatalf("optional synthetic examples rejected: %v", err)
	}
	for name, raw := range map[string]string{
		"missing language patterns": `{"limitations":[]}`,
		"null language patterns":    `{"language_patterns":null,"limitations":[]}`,
		"empty language patterns":   `{"language_patterns":[],"limitations":[]}`,
		"language patterns string":  `{"language_patterns":"x","limitations":[]}`,
		"non-string pattern":        `{"language_patterns":[42],"limitations":[]}`,
		"empty pattern":             `{"language_patterns":[""],"limitations":[]}`,
		"missing limitations":       `{"language_patterns":["x"]}`,
		"null limitations":          `{"language_patterns":["x"],"limitations":null}`,
		"limitations string":        `{"language_patterns":["x"],"limitations":"x"}`,
		"empty limitation":          `{"language_patterns":["x"],"limitations":[""]}`,
		"old summary":               `{"language_patterns":["x"],"limitations":[],"summary":"old"}`,
		"old tendencies":            `{"language_patterns":["x"],"limitations":[],"tendencies":["old"]}`,
		"old rhythm":                `{"language_patterns":["x"],"limitations":[],"rhythm_and_structure":"old"}`,
		"old warmth":                `{"language_patterns":["x"],"limitations":[],"warmth_and_directness":"old"}`,
		"old formatting":            `{"language_patterns":["x"],"limitations":[],"formatting_and_vocabulary_habits":["old"]}`,
		"old avoidance":             `{"language_patterns":["x"],"limitations":[],"things_to_avoid":["old"]}`,
		"old confidence":            `{"language_patterns":["x"],"limitations":[],"confidence":"old"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeContent(strings.NewReader(raw)); err == nil {
				t.Fatal("invalid sparse content passed validation")
			}
		})
	}
}

func TestEmptyLimitationsRoundTripLifecycleAndDigest(t *testing.T) {
	store, identity := testStore(t)
	content := Content{LanguagePatterns: []string{"Uses clipped clauses in updates"}, Limitations: []string{}}
	draft, err := store.CreateDraft(identity, content, testCoverage(), time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC))
	if err != nil || draft.Content.Limitations == nil || len(draft.Content.Limitations) != 0 || !hexDigestPattern.MatchString(draft.Digest) {
		t.Fatalf("draft = %#v, %v", draft, err)
	}
	path, _ := store.Path(identity)
	contents, _ := os.ReadFile(path)
	if !bytes.Contains(contents, []byte(`"limitations": []`)) {
		t.Fatalf("stored empty limitations collapsed: %s", contents)
	}
	reviewed, err := store.Review(identity)
	if err != nil || reviewed.Content.Limitations == nil || len(reviewed.Content.Limitations) != 0 || reviewed.Digest != draft.Digest {
		t.Fatalf("review = %#v, %v", reviewed, err)
	}
	approved, err := store.Approve(identity, draft.Digest, time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC))
	used, useErr := store.Use(identity)
	if err != nil || useErr != nil || approved.Content.Limitations == nil || used.Content.Limitations == nil || used.Digest != draft.Digest {
		t.Fatalf("approve/use = %#v, %#v, %v, %v", approved, used, err, useErr)
	}
}

func TestVersionOneDocumentIsIncompatibleBeforeContentDecodeAndUnchanged(t *testing.T) {
	store, identity := testStore(t)
	path, err := store.Path(identity)
	if err != nil {
		t.Fatal(err)
	}
	legacy := []byte("{\"schema_version\":1,\"draft\":{\"content\":{\"summary\":\"old persona\"}}}\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(identity)
	if err != nil || status.State != StateIncompatible {
		t.Fatalf("status = %#v, %v", status, err)
	}
	for name, operation := range map[string]func() error{
		"use":     func() error { _, err := store.Use(identity); return err },
		"review":  func() error { _, err := store.Review(identity); return err },
		"adjust":  func() error { _, err := store.Adjust(identity, testContent("replacement"), time.Now()); return err },
		"approve": func() error { _, err := store.Approve(identity, strings.Repeat("a", 64), time.Now()); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, ErrIncompatible) {
				t.Fatalf("operation error = %v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(got, legacy) {
				t.Fatalf("legacy profile changed: %q, %v", got, readErr)
			}
		})
	}
}
