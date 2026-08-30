// Package textformat applies explicitly enabled formatting modules to submitted Slack text.
package textformat

import (
	"strings"
	"unicode"
)

// Module identifies one opt-in text transformation.
type Module string

const (
	// ModuleEmDashToSpacedHyphen converts prose em dashes to spaced ASCII hyphens.
	ModuleEmDashToSpacedHyphen Module = "em-dash-to-spaced-hyphen"
)

var knownModules = map[Module]struct{}{
	ModuleEmDashToSpacedHyphen: {},
}

// Result contains formatted text and the modules that changed it.
type Result struct {
	Text    string
	Applied []Module
}

// ParseModule resolves a shipped formatting-module name.
func ParseModule(name string) (Module, bool) {
	module := Module(name)
	_, known := knownModules[module]
	return module, known
}

// Apply runs enabled modules in order and records only transformations that changed text.
func Apply(text string, modules []Module) Result {
	result := Result{Text: text}
	seen := make(map[Module]struct{}, len(modules))
	for _, module := range modules {
		if _, duplicate := seen[module]; duplicate {
			continue
		}
		seen[module] = struct{}{}

		var formatted string
		switch module {
		case ModuleEmDashToSpacedHyphen:
			formatted = emDashToSpacedHyphen(result.Text)
		default:
			continue
		}
		if formatted != result.Text {
			result.Text = formatted
			result.Applied = append(result.Applied, module)
		}
	}
	return result
}

// ApplyEdit formats one replacement with adjacent message context while leaving
// the rest of the existing body untouched. Start and end are byte offsets.
func ApplyEdit(body string, start, end int, replacement string, modules []Module) Result {
	left := body[:start]
	right := body[end:]
	formattedReplacement := replacement
	result := Result{}
	seen := make(map[Module]struct{}, len(modules))
	for _, module := range modules {
		if _, duplicate := seen[module]; duplicate {
			continue
		}
		seen[module] = struct{}{}
		switch module {
		case ModuleEmDashToSpacedHyphen:
			before := left + formattedReplacement + right
			left, formattedReplacement, right = formatEmDashReplacement(left, formattedReplacement, right)
			if left+formattedReplacement+right != before {
				result.Applied = append(result.Applied, module)
			}
		}
	}
	result.Text = left + formattedReplacement + right
	return result
}

// Names returns stable string names for receipts and help.
func Names(modules []Module) []string {
	names := make([]string, len(modules))
	for index, module := range modules {
		names[index] = string(module)
	}
	return names
}

// List renders modules for human-facing configuration and help.
func List(modules []Module) string {
	if len(modules) == 0 {
		return "none"
	}
	return strings.Join(Names(modules), ", ")
}

func emDashToSpacedHyphen(text string) string {
	return normalizeEmDashes(text, false, false)
}

func formatEmDashReplacement(left, replacement, right string) (string, string, string) {
	if !containsEmDash(replacement) {
		return left, replacement, right
	}
	leftInline := false
	rightInline := false
	if startsWithEmDash(replacement) {
		left = trimTrailingHorizontalSpace(left)
		leftRunes := []rune(left)
		leftInline = len(leftRunes) > 0 && !isLineBreak(leftRunes[len(leftRunes)-1])
	}
	if endsWithEmDash(replacement) {
		right = trimLeadingHorizontalSpace(right)
		rightRunes := []rune(right)
		rightInline = len(rightRunes) > 0 && !isLineBreak(rightRunes[0])
	}
	return left, normalizeEmDashes(replacement, leftInline, rightInline), right
}

func normalizeEmDashes(text string, leftInline, rightInline bool) string {
	runes := []rune(text)
	output := make([]rune, 0, len(runes))
	for index := 0; index < len(runes); index++ {
		if runes[index] != '—' {
			output = append(output, runes[index])
			continue
		}

		for len(output) > 0 && isHorizontalSpace(output[len(output)-1]) {
			output = output[:len(output)-1]
		}
		if (len(output) > 0 && !isLineBreak(output[len(output)-1])) || (len(output) == 0 && leftInline) {
			output = append(output, ' ')
		}
		output = append(output, '-')

		for index+1 < len(runes) && isHorizontalSpace(runes[index+1]) {
			index++
		}
		if (index+1 < len(runes) && !isLineBreak(runes[index+1])) || (index+1 == len(runes) && rightInline) {
			output = append(output, ' ')
		}
	}
	return string(output)
}

func containsEmDash(text string) bool {
	for _, character := range text {
		if character == '—' {
			return true
		}
	}
	return false
}

func startsWithEmDash(text string) bool {
	for _, character := range text {
		if isHorizontalSpace(character) {
			continue
		}
		return character == '—'
	}
	return false
}

func endsWithEmDash(text string) bool {
	runes := []rune(text)
	for index := len(runes) - 1; index >= 0; index-- {
		if isHorizontalSpace(runes[index]) {
			continue
		}
		return runes[index] == '—'
	}
	return false
}

func trimTrailingHorizontalSpace(text string) string {
	runes := []rune(text)
	for len(runes) > 0 && isHorizontalSpace(runes[len(runes)-1]) {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func trimLeadingHorizontalSpace(text string) string {
	runes := []rune(text)
	for len(runes) > 0 && isHorizontalSpace(runes[0]) {
		runes = runes[1:]
	}
	return string(runes)
}

func isHorizontalSpace(character rune) bool {
	return unicode.IsSpace(character) && !isLineBreak(character)
}

func isLineBreak(character rune) bool {
	switch character {
	case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
		return true
	default:
		return false
	}
}
