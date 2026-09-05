package cmd

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestNormalizeStyleEvidencePreservesEligibleUnmarkedText(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantText  string
		wantKinds []string
	}{
		{
			name:     "ordinary casing lines and paragraphs",
			raw:      "lowercase opening\ncontinues here\n\nfinal paragraph",
			wantText: "lowercase opening\ncontinues here\n\nfinal paragraph",
		},
		{
			name:      "line leading block quotes are omitted",
			raw:       "my intro\n> QUOTED PERSON\n>>> ALSO ONLY THIS LINE\nmy conclusion",
			wantText:  "my intro\nmy conclusion",
			wantKinds: []string{"blockquote_omitted"},
		},
		{
			name:     "escaped greater than remains literal prose",
			raw:      "&gt; literal comparison\nmy response &amp; conclusion",
			wantText: "> literal comparison\nmy response & conclusion",
		},
		{
			name:      "fenced and inline code are omitted",
			raw:       "before `INLINE_UPPER` words\n```\nFENCED_UPPER\n```\nafter",
			wantText:  "before words\nafter",
			wantKinds: []string{"fenced_code_omitted", "inline_code_omitted"},
		},
		{
			name:      "same line fenced code is omitted",
			raw:       "before ```FENCED_UPPER``` after",
			wantText:  "before after",
			wantKinds: []string{"fenced_code_omitted"},
		},
		{
			name:     "slack references locators credentials and controls are neutralized",
			raw:      "DOCUMENTATION asks <@U12345678> in <#C12345678|private> or <!subteam^S12345678> at https://example.com/a, email me@example.com with xoxp-secret-token and U87654321\x00now",
			wantText: "DOCUMENTATION asks [redacted] in [redacted] or [redacted] at [redacted], email [redacted] with [redacted] and [redacted] now",
		},
		{
			name:     "unicode format controls cannot split redaction targets",
			raw:      "safe\u202e text xoxp-\u200bsecret https://exa\u2066mple.com",
			wantText: "safe text [redacted] [redacted]",
		},
		{
			name:      "list like markers are removed but contents remain",
			raw:       "- first lower\n* second lower\n1. third lower\n2) fourth lower",
			wantText:  "first lower\nsecond lower\nthird lower\nfourth lower",
			wantKinds: []string{"bulleted_list_like", "numbered_list_like"},
		},
		{
			name:      "unmatched inline code omits only line remainder",
			raw:       "before `AMBIGUOUS\nnext line",
			wantText:  "before\nnext line",
			wantKinds: []string{"inline_code_omitted", "malformed_code_omitted"},
		},
		{
			name:      "unmatched fence omits message remainder",
			raw:       "before\n```\nAMBIGUOUS\nstill ambiguous",
			wantText:  "before",
			wantKinds: []string{"fenced_code_omitted", "malformed_code_omitted"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, eligible := normalizeStyleEvidence(test.raw)
			if !eligible {
				t.Fatalf("normalizeStyleEvidence() marked eligible input ineligible: %#v", got)
			}
			if got.UnmarkedText != test.wantText {
				t.Fatalf("unmarked_text = %q, want %q", got.UnmarkedText, test.wantText)
			}
			wantKinds := test.wantKinds
			if wantKinds == nil {
				wantKinds = []string{}
			}
			if !reflect.DeepEqual(got.DetectedStructure, wantKinds) {
				t.Fatalf("detected_structure = %#v, want %#v", got.DetectedStructure, wantKinds)
			}
			if !utf8.ValidString(got.UnmarkedText) {
				t.Fatalf("unmarked_text is invalid UTF-8: %q", got.UnmarkedText)
			}
			for _, character := range got.UnmarkedText {
				if unicode.Is(unicode.Cf, character) {
					t.Fatalf("unmarked_text contains Unicode format control %U: %q", character, got.UnmarkedText)
				}
			}
		})
	}
}

func TestNormalizeStyleEvidenceCanonicalizesStructureLabels(t *testing.T) {
	raw := "1. numbered\n- bullet\ntext `inline`\n> quote\n```fenced```\ntrailing `malformed"
	got, eligible := normalizeStyleEvidence(raw)
	if !eligible {
		t.Fatalf("normalizeStyleEvidence() unexpectedly ineligible: %#v", got)
	}
	want := []string{
		"blockquote_omitted",
		"fenced_code_omitted",
		"inline_code_omitted",
		"bulleted_list_like",
		"numbered_list_like",
		"malformed_code_omitted",
	}
	if !reflect.DeepEqual(got.DetectedStructure, want) {
		t.Fatalf("detected_structure = %#v, want %#v", got.DetectedStructure, want)
	}
}

func TestNormalizeStyleEvidenceRejectsContentFreeResults(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":            "",
		"whitespace":       " \n\t ",
		"quote only":       "> OTHER PERSON",
		"fenced code only": "```\nSECRET\n```",
		"inline code only": "`SECRET`",
		"URL only":         "https://example.com/private",
		"credential only":  "xoxp-private-token",
		"Slack ID only":    "U12345678",
	} {
		t.Run(name, func(t *testing.T) {
			got, eligible := normalizeStyleEvidence(raw)
			if eligible {
				t.Fatalf("normalizeStyleEvidence(%q) = eligible %#v", raw, got)
			}
		})
	}
}

func TestNormalizeStyleEvidenceKeepsProseAroundNeutralRedactions(t *testing.T) {
	got, eligible := normalizeStyleEvidence("please inspect https://example.com/private now")
	if !eligible || got.UnmarkedText != "please inspect [redacted] now" {
		t.Fatalf("normalizeStyleEvidence() = %#v, %t", got, eligible)
	}
	if strings.Contains(got.UnmarkedText, "example.com") {
		t.Fatalf("locator survived normalization: %q", got.UnmarkedText)
	}
}
