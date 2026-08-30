package textformat

import (
	"reflect"
	"testing"
)

func TestApplyDefaultsToExactIdentity(t *testing.T) {
	input := "exact—model expression – flags --verbose -5"
	result := Apply(input, nil)
	if result.Text != input || len(result.Applied) != 0 {
		t.Fatalf("Apply() = %#v", result)
	}
}

func TestEmDashToSpacedHyphen(t *testing.T) {
	module := []Module{ModuleEmDashToSpacedHyphen}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "unspaced", input: "em-dashes—kinda weird", want: "em-dashes - kinda weird"},
		{name: "already spaced", input: "em-dashes — kinda weird", want: "em-dashes - kinda weird"},
		{name: "left spaced", input: "em-dashes —kinda weird", want: "em-dashes - kinda weird"},
		{name: "right spaced", input: "em-dashes— kinda weird", want: "em-dashes - kinda weird"},
		{name: "unicode horizontal spaces", input: "em-dashes\u00a0—\u2003kinda weird", want: "em-dashes - kinda weird"},
		{name: "start boundary", input: "—kinda weird", want: "- kinda weird"},
		{name: "end boundary", input: "kinda weird—", want: "kinda weird -"},
		{name: "newlines preserved", input: "before\n—\nafter", want: "before\n-\nafter"},
		{name: "Unicode line separators preserved", input: "before\u2028—\u2029after", want: "before\u2028-\u2029after"},
		{name: "other dash forms unchanged", input: "range 1–3; flag --verbose; minus -5", want: "range 1–3; flag --verbose; minus -5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Apply(test.input, module)
			if result.Text != test.want {
				t.Fatalf("Apply(%q) = %q, want %q", test.input, result.Text, test.want)
			}
			wantApplied := []Module(nil)
			if test.input != test.want {
				wantApplied = module
			}
			if !reflect.DeepEqual(result.Applied, wantApplied) {
				t.Fatalf("applied = %v, want %v", result.Applied, wantApplied)
			}
		})
	}
}

func TestApplyEditUsesAdjacentContextWithoutFormattingExistingBody(t *testing.T) {
	module := []Module{ModuleEmDashToSpacedHyphen}
	tests := []struct {
		name        string
		body        string
		match       string
		replacement string
		modules     []Module
		want        string
		wantApplied []Module
	}{
		{
			name: "default exact", body: "wordXword", match: "X", replacement: "—",
			want: "word—word",
		},
		{
			name: "uses both sides", body: "wordXword", match: "X", replacement: "—", modules: module,
			want: "word - word", wantApplied: module,
		},
		{
			name: "collapses adjacent existing spaces", body: "word X word", match: "X", replacement: " — ", modules: module,
			want: "word - word", wantApplied: module,
		},
		{
			name: "preserves unrelated existing em dash", body: "existing—dash wordXword", match: "X", replacement: "—", modules: module,
			want: "existing—dash word - word", wantApplied: module,
		},
		{
			name: "message start", body: "Xword", match: "X", replacement: "—", modules: module,
			want: "- word", wantApplied: module,
		},
		{
			name: "message end", body: "wordX", match: "X", replacement: "—", modules: module,
			want: "word -", wantApplied: module,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := indexOf(t, test.body, test.match)
			result := ApplyEdit(test.body, start, start+len(test.match), test.replacement, test.modules)
			if result.Text != test.want || !reflect.DeepEqual(result.Applied, test.wantApplied) {
				t.Fatalf("ApplyEdit() = %#v, want text %q applied %v", result, test.want, test.wantApplied)
			}
		})
	}
}

func TestApplyDeduplicatesModules(t *testing.T) {
	result := Apply("a—b", []Module{ModuleEmDashToSpacedHyphen, ModuleEmDashToSpacedHyphen})
	if result.Text != "a - b" || !reflect.DeepEqual(result.Applied, []Module{ModuleEmDashToSpacedHyphen}) {
		t.Fatalf("Apply() = %#v", result)
	}
}

func indexOf(t *testing.T, body, match string) int {
	t.Helper()
	for index := 0; index+len(match) <= len(body); index++ {
		if body[index:index+len(match)] == match {
			return index
		}
	}
	t.Fatalf("match %q not found in %q", match, body)
	return -1
}

func TestParseModuleAndNames(t *testing.T) {
	module, known := ParseModule("em-dash-to-spaced-hyphen")
	if !known || module != ModuleEmDashToSpacedHyphen {
		t.Fatalf("ParseModule() = %q, %v", module, known)
	}
	if _, known := ParseModule("unknown"); known {
		t.Fatal("unknown module accepted")
	}
	if got := List([]Module{module}); got != "em-dash-to-spaced-hyphen" {
		t.Fatalf("List() = %q", got)
	}
	if got := List(nil); got != "none" {
		t.Fatalf("List(nil) = %q", got)
	}
}
