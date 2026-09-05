package cmd

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	styleStructureBlockquoteOmitted = "blockquote_omitted"
	styleStructureFencedCodeOmitted = "fenced_code_omitted"
	styleStructureInlineCodeOmitted = "inline_code_omitted"
	styleStructureBulletedListLike  = "bulleted_list_like"
	styleStructureNumberedListLike  = "numbered_list_like"
	styleStructureMalformedCode     = "malformed_code_omitted"
	styleEvidenceRedacted           = "[redacted]"
)

var (
	styleStructureOrder = []string{
		styleStructureBlockquoteOmitted,
		styleStructureFencedCodeOmitted,
		styleStructureInlineCodeOmitted,
		styleStructureBulletedListLike,
		styleStructureNumberedListLike,
		styleStructureMalformedCode,
	}
	styleBulletLikePattern      = regexp.MustCompile(`^[ \t]*(?:[-*•])[ \t]+`)
	styleNumberLikePattern      = regexp.MustCompile(`^[ \t]*[0-9]+[.)][ \t]+`)
	styleSlackReferencePattern  = regexp.MustCompile(`(?:<@[^>]+>|<#[^>]+>|<![^>]+>)`)
	styleSlackLocatorPattern    = regexp.MustCompile(`(?i)<(?:https?://|mailto:)[^>]+>`)
	styleSlackCredentialPattern = regexp.MustCompile(
		`(?i)(?:xox[a-z0-9]*(?:\.xox[a-z0-9]*)?|xapp|xwfp)-[A-Za-z0-9-]+`,
	)
	styleEmailPattern   = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
	styleBareURLPattern = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`)
	styleSlackIDPattern = regexp.MustCompile(`\b[UWBCGDFTES][A-Z0-9]{8,}\b`)
	styleSpacesPattern  = regexp.MustCompile(`[ \t]+`)
)

func normalizeStyleEvidence(raw string) (styleEvidenceMessage, bool) {
	cleaned := cleanStyleEvidenceControls(raw)

	labels := make(map[string]struct{}, len(styleStructureOrder))
	lines := strings.Split(cleaned, "\n")
	retained := make([]string, 0, len(lines))
	inFence := false

	for _, line := range lines {
		if !inFence && strings.HasPrefix(line, ">") {
			labels[styleStructureBlockquoteOmitted] = struct{}{}
			continue
		}

		withoutCode, omitted := omitStyleEvidenceCode(line, &inFence, labels)
		withoutCode = stripStyleListMarker(withoutCode, labels)
		withoutCode = sanitizeStyleEvidenceText(withoutCode)
		withoutCode = strings.TrimSpace(styleSpacesPattern.ReplaceAllString(withoutCode, " "))

		switch {
		case withoutCode != "":
			retained = append(retained, withoutCode)
		case !omitted && strings.TrimSpace(line) == "":
			retained = append(retained, "")
		}
	}
	if inFence {
		labels[styleStructureMalformedCode] = struct{}{}
	}

	unmarkedText := joinStyleEvidenceLines(retained)
	message := styleEvidenceMessage{
		UnmarkedText:      unmarkedText,
		DetectedStructure: orderedStyleStructure(labels),
	}
	return message, hasStyleEvidenceContent(unmarkedText)
}

func cleanStyleEvidenceControls(raw string) string {
	raw = strings.ToValidUTF8(raw, " ")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	var cleaned strings.Builder
	cleaned.Grow(len(raw))
	for _, character := range raw {
		switch {
		case unicode.Is(unicode.Cf, character):
			continue
		case character == '\n':
			cleaned.WriteRune(character)
		case character == '\t':
			cleaned.WriteByte(' ')
		case unicode.IsControl(character):
			cleaned.WriteByte(' ')
		default:
			cleaned.WriteRune(character)
		}
	}
	return cleaned.String()
}

func omitStyleEvidenceCode(line string, inFence *bool, labels map[string]struct{}) (string, bool) {
	var retained strings.Builder
	omitted := false

	for offset := 0; offset < len(line); {
		if *inFence {
			labels[styleStructureFencedCodeOmitted] = struct{}{}
			omitted = true
			closing := strings.Index(line[offset:], "```")
			if closing < 0 {
				return retained.String(), omitted
			}
			offset += closing + 3
			*inFence = false
			continue
		}

		opener := strings.IndexByte(line[offset:], '`')
		if opener < 0 {
			retained.WriteString(line[offset:])
			break
		}
		opener += offset
		retained.WriteString(line[offset:opener])

		if strings.HasPrefix(line[opener:], "```") {
			labels[styleStructureFencedCodeOmitted] = struct{}{}
			omitted = true
			closing := strings.Index(line[opener+3:], "```")
			if closing < 0 {
				*inFence = true
				return retained.String(), omitted
			}
			offset = opener + 3 + closing + 3
			continue
		}

		labels[styleStructureInlineCodeOmitted] = struct{}{}
		omitted = true
		closing := strings.IndexByte(line[opener+1:], '`')
		if closing < 0 {
			labels[styleStructureMalformedCode] = struct{}{}
			return retained.String(), omitted
		}
		offset = opener + 1 + closing + 1
	}

	return retained.String(), omitted
}

func stripStyleListMarker(line string, labels map[string]struct{}) string {
	if match := styleBulletLikePattern.FindStringIndex(line); match != nil {
		labels[styleStructureBulletedListLike] = struct{}{}
		return line[match[1]:]
	}
	if match := styleNumberLikePattern.FindStringIndex(line); match != nil {
		labels[styleStructureNumberedListLike] = struct{}{}
		return line[match[1]:]
	}
	return line
}

func sanitizeStyleEvidenceText(text string) string {
	text = styleSlackReferencePattern.ReplaceAllString(text, styleEvidenceRedacted)
	text = styleSlackLocatorPattern.ReplaceAllString(text, styleEvidenceRedacted)
	text = styleSlackCredentialPattern.ReplaceAllString(text, styleEvidenceRedacted)
	text = styleEmailPattern.ReplaceAllString(text, styleEvidenceRedacted)
	text = styleBareURLPattern.ReplaceAllStringFunc(text, redactStyleBareURL)
	text = styleSlackIDPattern.ReplaceAllStringFunc(text, redactStyleSlackID)
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">").Replace(text)
}

func redactStyleSlackID(value string) string {
	for _, character := range value {
		if unicode.IsDigit(character) {
			return styleEvidenceRedacted
		}
	}
	return value
}

func redactStyleBareURL(value string) string {
	cut := len(value)
	for cut > 0 && strings.ContainsRune(".,;:!?)]}", rune(value[cut-1])) {
		cut--
	}
	return styleEvidenceRedacted + value[cut:]
}

func orderedStyleStructure(labels map[string]struct{}) []string {
	ordered := make([]string, 0, len(labels))
	for _, label := range styleStructureOrder {
		if _, present := labels[label]; present {
			ordered = append(ordered, label)
		}
	}
	return ordered
}

func joinStyleEvidenceLines(lines []string) string {
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}

	compacted := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" && len(compacted) > 0 && compacted[len(compacted)-1] == "" {
			continue
		}
		compacted = append(compacted, line)
	}
	return strings.Join(compacted, "\n")
}

func hasStyleEvidenceContent(text string) bool {
	text = strings.ReplaceAll(text, styleEvidenceRedacted, "")
	for _, character := range text {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || unicode.IsSymbol(character) {
			return true
		}
	}
	return false
}
