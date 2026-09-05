package format

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/api"
)

func TestProjectMessageContentProjectsSupportedBlocksInOrder(t *testing.T) {
	message := api.Message{Blocks: []json.RawMessage{
		contentRaw(t, map[string]interface{}{
			"type":   "section",
			"text":   map[string]string{"type": "mrkdwn", "text": "first <@U1>"},
			"fields": []interface{}{map[string]string{"type": "plain_text", "text": "field"}},
		}),
		contentRaw(t, map[string]interface{}{
			"type": "context",
			"elements": []interface{}{
				map[string]string{"type": "mrkdwn", "text": "context one"},
				map[string]string{"type": "plain_text", "text": "context two"},
			},
		}),
		contentRaw(t, map[string]interface{}{
			"type": "rich_text",
			"elements": []interface{}{
				map[string]interface{}{"type": "rich_text_section", "elements": []interface{}{
					map[string]string{"type": "text", "text": "rich "},
					map[string]string{"type": "link", "text": "link", "url": "https://example.test"},
					map[string]string{"type": "emoji", "name": "wave"},
					map[string]string{"type": "user", "user_id": "U2"},
					map[string]string{"type": "channel", "channel_id": "C1"},
					map[string]string{"type": "broadcast", "range": "here"},
					map[string]string{"type": "usergroup", "usergroup_id": "S1"},
					map[string]string{"type": "date", "fallback": "tomorrow"},
					map[string]interface{}{"type": "text", "text": "code", "style": map[string]bool{"code": true}},
				}},
				map[string]interface{}{"type": "rich_text_quote", "elements": []interface{}{map[string]string{"type": "text", "text": "quoted"}}},
				map[string]interface{}{"type": "rich_text_preformatted", "language": "go", "elements": []interface{}{map[string]string{"type": "text", "text": "fmt.Println()"}}},
				map[string]interface{}{"type": "rich_text_list", "style": "ordered", "indent": 1, "elements": []interface{}{
					map[string]interface{}{"type": "rich_text_section", "elements": []interface{}{map[string]string{"type": "text", "text": "one"}}},
					map[string]interface{}{"type": "rich_text_section", "elements": []interface{}{map[string]string{"type": "text", "text": "two"}}},
				}},
			},
		}),
	}}
	resolve := func(id string) string {
		return map[string]string{"U1": "one", "U2": "two", "C1": "general", "S1": "team"}[id]
	}

	got := ProjectMessageContent(message, resolve)
	if got.Representation != HistoryBlocks || got.CompositionProvenance != "unknown" || len(got.Exceptions) != 0 {
		t.Fatalf("projection metadata = %#v", got)
	}
	want := []MessageContentPartJSON{
		{Kind: PartText, Text: "first @one"},
		{Kind: PartText, Text: "field"},
		{Kind: PartContext, Text: "context one"},
		{Kind: PartContext, Text: "context two"},
		{Kind: PartText, Text: "rich link (https://example.test):wave:@two#C1@here@S1tomorrow`code`"},
		{Kind: PartQuote, Text: "quoted"},
		{Kind: PartCode, Text: "fmt.Println()", Language: "go"},
		{Kind: PartList, ListStyle: ListOrdered, Indent: 1, Items: []string{"one", "two"}},
	}
	if !reflect.DeepEqual(got.Parts, want) {
		t.Fatalf("parts = %#v, want %#v", got.Parts, want)
	}
}

func TestProjectMessageContentBlocksAreAuthoritativeAndRetainBlockOnly(t *testing.T) {
	message := api.Message{
		Text: "transport fallback must not appear",
		Blocks: []json.RawMessage{contentRaw(t, map[string]interface{}{
			"type": "section",
			"text": map[string]string{"type": "mrkdwn", "text": "block body"},
		})},
	}
	got := ProjectMessageContent(message, nil)
	if got.Representation != HistoryBlocks || got.FallbackText != "" || len(got.Parts) != 1 || got.Parts[0].Text != "block body" {
		t.Fatalf("block authority lost: %#v", got)
	}

	blockOnly := ProjectMessageContent(api.Message{Blocks: message.Blocks}, nil)
	if len(blockOnly.Parts) != 1 || blockOnly.Parts[0].Text != "block body" {
		t.Fatalf("block-only content was lost: %#v", blockOnly)
	}
}

func TestProjectMessageContentSegmentsFallbackSyntax(t *testing.T) {
	got := ProjectMessageContent(api.Message{Text: "prose\n> quoted\n> next\n```go\nfmt.Println()\n```\n- one\n- two\n  1. nested"}, nil)
	want := []MessageContentPartJSON{
		{Kind: PartText, Text: "prose"},
		{Kind: PartQuote, Text: "quoted\nnext"},
		{Kind: PartCode, Text: "fmt.Println()", Language: "go"},
		{Kind: PartList, ListStyle: ListBullet, Indent: 0, Items: []string{"one", "two"}},
		{Kind: PartList, ListStyle: ListOrdered, Indent: 2, Items: []string{"nested"}},
	}
	if got.Representation != HistoryFallbackText || !reflect.DeepEqual(got.Parts, want) {
		t.Fatalf("fallback projection = %#v, want %#v", got, want)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"list_style":"bullet","indent":0`) {
		t.Fatalf("zero list indent was omitted: %s", encoded)
	}
}

func TestProjectSearchContentIsTextOnly(t *testing.T) {
	got := ProjectSearchContent(api.SearchMatch{User: "", Username: "robot", Text: "> snippet"}, nil)
	if got.Representation != SearchTextOnly || got.CompositionProvenance != "unknown" || len(got.Exceptions) != 0 {
		t.Fatalf("search representation = %#v", got)
	}
	if want := []MessageContentPartJSON{{Kind: PartQuote, Text: "snippet"}}; !reflect.DeepEqual(got.Parts, want) {
		t.Fatalf("search content = %#v, want %#v", got.Parts, want)
	}
	if AuthorKindForSearch(api.SearchMatch{Username: "robot"}) != AuthorUnknown {
		t.Fatal("search username inferred a bot author")
	}
}

func TestProjectMessageContentReportsMalformedAndUnsupportedWithoutPanic(t *testing.T) {
	message := api.Message{
		Text: "fallback <@U1>",
		Blocks: []json.RawMessage{
			json.RawMessage(`{`),
			contentRaw(t, map[string]interface{}{"type": "actions"}),
			contentRaw(t, map[string]interface{}{"type": "rich_text", "elements": []interface{}{
				map[string]interface{}{"type": "rich_text_section", "elements": []interface{}{
					map[string]string{"type": "text", "text": "kept"},
					map[string]string{"type": "image", "image_url": "private"},
				}},
			}}),
		},
	}
	got := ProjectMessageContent(message, func(string) string { return "owner" })
	if got.Representation != HistoryBlocksPartial || got.FallbackText != "" {
		t.Fatalf("partial block projection = %#v", got)
	}
	if len(got.Parts) != 1 || got.Parts[0].Text != "kept" || len(got.Exceptions) != 3 {
		t.Fatalf("recognized content or exceptions wrong: %#v", got)
	}
	for _, exception := range got.Exceptions {
		if strings.Contains(exception.SourceType, "private") {
			t.Fatalf("exception exposed payload: %#v", exception)
		}
	}
}

func TestProjectMessageContentPartialFallbackOnlyWithoutBody(t *testing.T) {
	partial := api.Message{
		Text: "fallback <@U1>\x00",
		Blocks: []json.RawMessage{contentRaw(t, map[string]interface{}{
			"type":     "context",
			"elements": []interface{}{map[string]string{"type": "plain_text", "text": "context only"}},
		}), contentRaw(t, map[string]interface{}{"type": "divider"})},
	}
	got := ProjectMessageContent(partial, func(string) string { return "owner" })
	if got.Representation != HistoryBlocksPartial || got.FallbackText != "fallback @owner" {
		t.Fatalf("partial fallback = %#v", got)
	}
	if len(got.Parts) != 1 || got.Parts[0].Kind != PartContext {
		t.Fatalf("context content = %#v", got.Parts)
	}

	noFallback := ProjectMessageContent(api.Message{Blocks: partial.Blocks}, nil)
	if noFallback.FallbackText != "" {
		t.Fatalf("empty transport text yielded fallback: %#v", noFallback)
	}
}

func TestContentMetadataMatrices(t *testing.T) {
	messageCases := []struct {
		message api.Message
		want    AuthorKind
		role    ThreadRole
	}{
		{api.Message{BotID: "B1", User: "U1"}, AuthorBot, ThreadTopLevel},
		{api.Message{User: "U1"}, AuthorSlackUser, ThreadTopLevel},
		{api.Message{}, AuthorUnknown, ThreadTopLevel},
		{api.Message{Ts: "1", ThreadTs: "1", ReplyCount: 2}, AuthorUnknown, ThreadParent},
		{api.Message{Ts: "2", ThreadTs: "1"}, AuthorUnknown, ThreadReply},
		{api.Message{Ts: "1", ThreadTs: "1"}, AuthorUnknown, ThreadParent},
		{api.Message{ReplyCount: 1}, AuthorUnknown, ThreadParent},
	}
	for _, test := range messageCases {
		if got := AuthorKindForMessage(test.message); got != test.want {
			t.Errorf("AuthorKindForMessage(%#v) = %q, want %q", test.message, got, test.want)
		}
		if got := ThreadRoleForMessage(test.message); got != test.role {
			t.Errorf("ThreadRoleForMessage(%#v) = %q, want %q", test.message, got, test.role)
		}
	}
}

func TestProjectMessageContentResolvesWithoutMutatingTransportAndAlwaysUsesArray(t *testing.T) {
	original := "hello <@U1>\x1b[31m"
	message := api.Message{Text: original}
	got := ProjectMessageContent(message, func(string) string { return "owner" })
	if message.Text != original {
		t.Fatalf("projector mutated transport text: %q", message.Text)
	}
	if len(got.Parts) != 1 || got.Parts[0].Text != "hello @owner" {
		t.Fatalf("resolved/cleaned semantic content = %#v", got.Parts)
	}

	empty, err := json.Marshal(ProjectMessageContent(api.Message{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(empty), `"parts":[]`) {
		t.Fatalf("empty parts encoded as null: %s", empty)
	}
}

func TestMessageContentPartJSONUsesKindDiscriminator(t *testing.T) {
	encoded, err := json.Marshal(MessageContentPartJSON{Kind: PartText, Text: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); !strings.Contains(got, `"kind":"text"`) || strings.Contains(got, `"type"`) {
		t.Fatalf("semantic part discriminator = %s", got)
	}
}

func TestProjectMessageContentClassifiesRawSyntaxBeforeResolution(t *testing.T) {
	message := api.Message{Text: "&gt; literal\n> quote <@U1>\n- item <@U1>\n```\n<@U1> &gt;\n```"}
	got := ProjectMessageContent(message, func(id string) string { return "person" })
	want := []MessageContentPartJSON{
		{Kind: PartText, Text: "> literal"},
		{Kind: PartQuote, Text: "quote @person"},
		{Kind: PartList, ListStyle: ListBullet, Indent: 0, Items: []string{"item @person"}},
		{Kind: PartCode, Text: "<@U1> &gt;"},
	}
	if !reflect.DeepEqual(got.Parts, want) {
		t.Fatalf("raw syntax classification = %#v, want %#v", got.Parts, want)
	}
}

func TestSemanticTextCleaningPreservesNewlinesAndLegacyText(t *testing.T) {
	original := string([]byte{'a', 0x1b, '[', '3', '1', 'm', 0xe2, 0x80, 0x8b, 0x00, 0xff, 'b', '\r', '\n', 'c'})
	message := api.Message{Text: original}
	got := ProjectMessageContent(message, nil)
	if message.Text != original {
		t.Fatalf("legacy text changed: %q", message.Text)
	}
	if want := []MessageContentPartJSON{{Kind: PartText, Text: "a\u200bb\nc"}}; !reflect.DeepEqual(got.Parts, want) {
		t.Fatalf("clean semantic parts = %#v, want %#v", got.Parts, want)
	}
}

func TestSemanticContentPreservesUnicodeComposition(t *testing.T) {
	for _, test := range []struct{ name, text string }{
		{"joined emoji", "Ship it \U0001f469\u200d\U0001f4bb"},
		{"Persian word", "می\u200cروم"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rich := api.Message{Blocks: []json.RawMessage{contentRaw(t, map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{map[string]interface{}{
					"type":     "rich_text_section",
					"elements": []interface{}{map[string]string{"type": "text", "text": test.text}},
				}},
			})}}
			for source, content := range map[string]MessageContentJSON{
				"history fallback": ProjectMessageContent(api.Message{Text: test.text}, nil),
				"rich history":     ProjectMessageContent(rich, nil),
				"search":           ProjectSearchContent(api.SearchMatch{Text: test.text}, nil),
			} {
				t.Run(source, func(t *testing.T) {
					want := []MessageContentPartJSON{{Kind: PartText, Text: test.text}}
					if !reflect.DeepEqual(content.Parts, want) {
						t.Fatalf("projection changed Unicode composition: %#v, want %#v", content.Parts, want)
					}
					encoded, err := json.Marshal(content)
					if err != nil {
						t.Fatal(err)
					}
					var decoded MessageContentJSON
					if err := json.Unmarshal(encoded, &decoded); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(decoded.Parts, want) {
						t.Fatalf("semantic JSON changed Unicode composition: %s", encoded)
					}
					if rendered := RenderSemanticContent(content, "  ", "", "  "); !strings.Contains(rendered, "│ "+test.text+"\n") {
						t.Fatalf("human rendering changed Unicode composition: %q", rendered)
					}
				})
			}
		})
	}
}

func TestProjectMessageContentReportsSectionAccessories(t *testing.T) {
	message := api.Message{Blocks: []json.RawMessage{
		contentRaw(t, map[string]interface{}{
			"type":      "section",
			"text":      map[string]string{"type": "plain_text", "text": "body"},
			"accessory": map[string]string{"type": "button", "text": "open"},
		}),
		contentRaw(t, map[string]interface{}{
			"type":      "section",
			"text":      map[string]string{"type": "plain_text", "text": "other"},
			"accessory": map[string]interface{}{},
		}),
	}}
	got := ProjectMessageContent(message, nil)
	if got.Representation != HistoryBlocksPartial {
		t.Fatalf("accessories silently treated complete: %#v", got)
	}
	want := []MessageContentExceptionJSON{
		{Scope: ExceptionElement, SourceType: "button", Reason: ExceptionUnsupported},
		{Scope: ExceptionElement, SourceType: "unknown", Reason: ExceptionMalformed},
	}
	if !reflect.DeepEqual(got.Exceptions, want) {
		t.Fatalf("accessory exceptions = %#v, want %#v", got.Exceptions, want)
	}
}

func TestRichTextReferencesAndLinks(t *testing.T) {
	calls := make([]string, 0)
	message := api.Message{Blocks: []json.RawMessage{contentRaw(t, map[string]interface{}{
		"type": "rich_text",
		"elements": []interface{}{map[string]interface{}{
			"type": "rich_text_section",
			"elements": []interface{}{
				map[string]string{"type": "user", "user_id": "U1"},
				map[string]string{"type": "channel", "channel_id": "C1"},
				map[string]string{"type": "usergroup", "usergroup_id": "S1"},
				map[string]string{"type": "link", "url": "https://one.test"},
				map[string]string{"type": "link", "text": "two", "url": "https://two.test"},
			},
		}},
	})}}
	got := ProjectMessageContent(message, func(id string) string {
		calls = append(calls, id)
		return "person"
	})
	if want := []string{"U1"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("resolveUser calls = %#v, want %#v", calls, want)
	}
	if want := "@person#C1@S1https://one.testtwo (https://two.test)"; len(got.Parts) != 1 || got.Parts[0].Text != want {
		t.Fatalf("rich references/links = %#v, want %q", got.Parts, want)
	}
}

func TestProjectMessageContentReportsUnusableRequiredContent(t *testing.T) {
	message := api.Message{Blocks: []json.RawMessage{
		contentRaw(t, map[string]interface{}{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": ""}}),
		contentRaw(t, map[string]interface{}{"type": "rich_text", "elements": []interface{}{
			map[string]interface{}{"type": "rich_text_section", "elements": []interface{}{map[string]string{"type": "text", "text": ""}}},
		}}),
	}}
	got := ProjectMessageContent(message, nil)
	if got.Representation != HistoryBlocksPartial || len(got.Parts) != 0 {
		t.Fatalf("unusable content was interpreted: %#v", got)
	}
	for _, exception := range got.Exceptions {
		if exception.Reason == ExceptionMalformed {
			return
		}
	}
	t.Fatalf("unusable content omitted no malformed exception: %#v", got.Exceptions)
}

func TestSegmentTextRecognizesContractBulletsOnly(t *testing.T) {
	got := ProjectMessageContent(api.Message{Text: "+ not a bullet\n• a bullet"}, nil)
	want := []MessageContentPartJSON{
		{Kind: PartText, Text: "+ not a bullet"},
		{Kind: PartList, ListStyle: ListBullet, Indent: 0, Items: []string{"a bullet"}},
	}
	if !reflect.DeepEqual(got.Parts, want) {
		t.Fatalf("bullet recognition = %#v, want %#v", got.Parts, want)
	}
}

func TestBoundedSourceTypeAndExceptionTruncation(t *testing.T) {
	for _, sourceType := range []string{"rich_text_list", "a-1"} {
		if got := boundedSourceType(sourceType); got != sourceType {
			t.Fatalf("boundedSourceType(%q) = %q", sourceType, got)
		}
	}
	for _, sourceType := range []string{"Upper", "has space", strings.Repeat("a", 65)} {
		if got := boundedSourceType(sourceType); got != "unknown" {
			t.Fatalf("boundedSourceType(%q) = %q, want unknown", sourceType, got)
		}
	}

	exactBlocks := make([]json.RawMessage, maxContentExceptions)
	for i := range exactBlocks {
		exactBlocks[i] = contentRaw(t, map[string]string{"type": "actions"})
	}
	exact := ProjectMessageContent(api.Message{Blocks: exactBlocks}, nil)
	if got := exact.Exceptions[len(exact.Exceptions)-1].SourceType; got != "actions" {
		t.Fatalf("exact exception cap was truncated: %#v", exact.Exceptions)
	}

	blocks := make([]json.RawMessage, maxContentExceptions+2)
	for i := range blocks {
		blocks[i] = contentRaw(t, map[string]string{"type": "actions"})
	}
	got := ProjectMessageContent(api.Message{Blocks: blocks}, nil)
	if len(got.Exceptions) != maxContentExceptions {
		t.Fatalf("exception cap = %d, want %d", len(got.Exceptions), maxContentExceptions)
	}
	if want := (MessageContentExceptionJSON{Scope: ExceptionElement, SourceType: "truncated", Reason: ExceptionUnsupported}); got.Exceptions[len(got.Exceptions)-1] != want {
		t.Fatalf("truncation marker = %#v, want %#v", got.Exceptions[len(got.Exceptions)-1], want)
	}
}

func TestRenderSemanticContentKeepsOrdinaryMultilineCompactAndContained(t *testing.T) {
	content := MessageContentJSON{
		Representation:        HistoryFallbackText,
		CompositionProvenance: "unknown",
		Parts:                 []MessageContentPartJSON{{Kind: PartText, Text: "first line\nsecond line"}},
	}
	got := RenderSemanticContent(content, "  @alex: ", "  @alex:", "    ")
	want := "  @alex: │ first line\n    │ second line\n    [history fallback text; block structure unavailable]\n"
	if got != want {
		t.Fatalf("compact render = %q, want %q", got, want)
	}
}

func TestRenderSemanticContentLabelsStructureAndBoundedLoss(t *testing.T) {
	content := MessageContentJSON{
		Representation:        HistoryBlocksPartial,
		CompositionProvenance: "unknown",
		Parts: []MessageContentPartJSON{
			{Kind: PartContext, Text: "assisted response"},
			{Kind: PartQuote, Text: "first\nsecond"},
			{Kind: PartCode, Text: "go test ./...", Language: "go"},
			{Kind: PartList, ListStyle: ListOrdered, Indent: 0, Items: []string{"inspect", "verify\nagain"}},
		},
		Exceptions:   []MessageContentExceptionJSON{{Scope: ExceptionBlock, SourceType: "actions", Reason: ExceptionUnsupported}},
		FallbackText: "fallback body",
	}
	got := RenderSemanticContent(content, "  @alex: ", "  @alex:", "    ")
	want := "  @alex:\n" +
		"    [partial block content]\n" +
		"    [context] │ assisted response\n" +
		"    [quote]\n" +
		"      │ first\n" +
		"      │ second\n" +
		"    [code]\n" +
		"      │ go test ./...\n" +
		"    [list: ordered]\n" +
		"      1. │ inspect\n" +
		"      2. │ verify\n" +
		"         │ again\n" +
		"    [unsupported block: actions]\n" +
		"    [fallback approximation]\n" +
		"      │ fallback body\n"
	if got != want {
		t.Fatalf("structured render = %q, want %q", got, want)
	}
}

func TestRenderSemanticContentDoesNotRepeatSearchLimitation(t *testing.T) {
	content := MessageContentJSON{
		Representation:        SearchTextOnly,
		CompositionProvenance: "unknown",
		Parts:                 []MessageContentPartJSON{{Kind: PartText, Text: "search result"}},
	}
	got := RenderSemanticContent(content, "  @alex: ", "  @alex:", "    ")
	if got != "  @alex: │ search result\n" || strings.Contains(got, SearchContentNotice) {
		t.Fatalf("search render repeated package-level limitation: %q", got)
	}
}

func TestEmptyRequiredBlockElementsUseLabelledFallback(t *testing.T) {
	for _, blockType := range []string{"context", "rich_text"} {
		t.Run(blockType, func(t *testing.T) {
			message := api.Message{
				Text: "fallback <@U1>",
				Blocks: []json.RawMessage{contentRaw(t, map[string]interface{}{
					"type": blockType, "elements": []interface{}{},
				})},
			}
			got := ProjectMessageContent(message, func(string) string { return "owner" })
			if got.Representation != HistoryBlocksPartial || got.FallbackText != "fallback @owner" || len(got.Parts) != 0 {
				t.Fatalf("empty %s projection = %#v", blockType, got)
			}
			if len(got.Exceptions) != 1 || got.Exceptions[0].Scope != ExceptionBlock || got.Exceptions[0].Reason != ExceptionMalformed {
				t.Fatalf("empty %s exceptions = %#v", blockType, got.Exceptions)
			}
			rendered := RenderSemanticContent(got, "  @owner: ", "  @owner:", "    ")
			if !strings.Contains(rendered, "[fallback approximation]\n      │ fallback @owner") {
				t.Fatalf("empty %s silently lost fallback:\n%s", blockType, rendered)
			}
		})
	}
}

func TestSemanticListIndentIsBoundedBeforeRendering(t *testing.T) {
	message := api.Message{Blocks: []json.RawMessage{contentRaw(t, map[string]interface{}{
		"type": "rich_text",
		"elements": []interface{}{map[string]interface{}{
			"type": "rich_text_list", "style": "bullet", "indent": 999999999,
			"elements": []interface{}{map[string]interface{}{
				"type":     "rich_text_section",
				"elements": []interface{}{map[string]string{"type": "text", "text": "item"}},
			}},
		}},
	})}}
	got := ProjectMessageContent(message, nil)
	if got.Representation != HistoryBlocksPartial || len(got.Parts) != 0 || len(got.Exceptions) != 1 || got.Exceptions[0].Reason != ExceptionMalformed {
		t.Fatalf("extreme rich-list indent = %#v", got)
	}

	overIndented := ProjectMessageContent(api.Message{Text: strings.Repeat(" ", maxSemanticListIndent+1) + "- item"}, nil)
	if len(overIndented.Parts) != 1 || overIndented.Parts[0].Kind != PartText {
		t.Fatalf("over-indented fallback was classified as a list: %#v", overIndented.Parts)
	}

	defensive := MessageContentJSON{
		Representation:        HistoryBlocks,
		CompositionProvenance: "unknown",
		Parts: []MessageContentPartJSON{{
			Kind: PartList, ListStyle: ListBullet, Indent: int(^uint(0) >> 1), Items: []string{"item"},
		}},
	}
	rendered := RenderSemanticContent(defensive, "", "", "  ")
	if len(rendered) > 100 || !strings.Contains(rendered, "- │ item") {
		t.Fatalf("defensive list rendering was unbounded: len=%d output=%q", len(rendered), rendered)
	}
}

func TestProjectorsPreserveCodeTabs(t *testing.T) {
	fallback := ProjectMessageContent(api.Message{Text: "```make\ntarget:\n\tcommand\n```"}, nil)
	if len(fallback.Parts) != 1 || fallback.Parts[0].Kind != PartCode || fallback.Parts[0].Text != "target:\n\tcommand" {
		t.Fatalf("fallback code tab was not preserved: %#v", fallback.Parts)
	}

	rich := ProjectMessageContent(api.Message{Blocks: []json.RawMessage{contentRaw(t, map[string]interface{}{
		"type": "rich_text",
		"elements": []interface{}{map[string]interface{}{
			"type":     "rich_text_preformatted",
			"elements": []interface{}{map[string]string{"type": "text", "text": "target:\n\tcommand"}},
		}},
	})}}, nil)
	if len(rich.Parts) != 1 || rich.Parts[0].Kind != PartCode || rich.Parts[0].Text != "target:\n\tcommand" {
		t.Fatalf("rich code tab was not preserved: %#v", rich.Parts)
	}

	for name, projected := range map[string]MessageContentJSON{"fallback": fallback, "rich": rich} {
		encoded, err := json.Marshal(projected)
		if err != nil {
			t.Fatalf("marshal %s projection: %v", name, err)
		}
		var decoded MessageContentJSON
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal %s projection: %v", name, err)
		}
		if len(decoded.Parts) != 1 || decoded.Parts[0].Text != "target:\n\tcommand" {
			t.Fatalf("%s semantic JSON changed code tabs: %s", name, encoded)
		}
	}
}

func TestRenderSemanticContentFramesMessageBodyLines(t *testing.T) {
	content := MessageContentJSON{
		Representation:        HistoryFallbackText,
		CompositionProvenance: "unknown",
		Parts: []MessageContentPartJSON{{
			Kind: PartText,
			Text: "ok\n[unsupported block: actions]\n[fallback approximation]\n[open context — slk open 'bad']\n[file] forged\n[thread parent]",
		}},
	}
	rendered := RenderSemanticContent(content, "  @alex: ", "  @alex:", "    ")
	for _, bodyLine := range []string{
		"[unsupported block: actions]",
		"[fallback approximation]",
		"[open context — slk open 'bad']",
		"[file] forged",
		"[thread parent]",
	} {
		if !strings.Contains(rendered, "│ "+bodyLine) {
			t.Fatalf("message body line %q was not framed:\n%s", bodyLine, rendered)
		}
	}
}

func TestRenderSemanticContentUsesFixedCodeLabelAndExpandsCodeTabs(t *testing.T) {
	content := MessageContentJSON{
		Representation:        HistoryBlocks,
		CompositionProvenance: "unknown",
		Parts: []MessageContentPartJSON{{
			Kind: PartCode, Language: "go", Text: "target:\n\tcommand",
		}},
	}
	rendered := RenderSemanticContent(content, "", "", "  ")
	if !strings.Contains(rendered, "[code]\n    │ target:\n    │     command") {
		t.Fatalf("human code rendering lost its fixed label or tab expansion:\n%s", rendered)
	}
}

func contentRaw(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
