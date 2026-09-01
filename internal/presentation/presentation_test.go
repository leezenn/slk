package presentation

import (
	"encoding/json"
	"testing"
)

func rawBlock(t *testing.T, value map[string]interface{}) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestEffectiveDefaultsAndRejectsUnknownModes(t *testing.T) {
	if got, err := Effective(""); err != nil || got != SlackManaged {
		t.Fatalf("Effective(omitted) = %q, %v", got, err)
	}
	if got, err := Effective(AlwaysExpanded); err != nil || got != AlwaysExpanded {
		t.Fatalf("Effective(always-expanded) = %q, %v", got, err)
	}
	if _, err := Effective("forced"); err == nil {
		t.Fatal("Effective(unknown) unexpectedly succeeded")
	}
}

func TestDetectBlocksNormalizesOnlyConsistentLayouts(t *testing.T) {
	tests := []struct {
		name   string
		blocks []json.RawMessage
		want   Mode
		known  bool
	}{
		{name: "no block data is unknown"},
		{
			name:   "native rich text is slack managed",
			blocks: []json.RawMessage{rawBlock(t, map[string]interface{}{"type": "rich_text"})},
			want:   SlackManaged,
			known:  true,
		},
		{
			name: "generated omitted expand is slack managed",
			blocks: []json.RawMessage{
				rawBlock(t, map[string]interface{}{"type": "context"}),
				rawBlock(t, map[string]interface{}{"type": "section"}),
				rawBlock(t, map[string]interface{}{"type": "section", "expand": false}),
			},
			want:  SlackManaged,
			known: true,
		},
		{
			name: "uniform expanded sections",
			blocks: []json.RawMessage{
				rawBlock(t, map[string]interface{}{"type": "section", "expand": true}),
				rawBlock(t, map[string]interface{}{"type": "section", "expand": true}),
			},
			want:  AlwaysExpanded,
			known: true,
		},
		{
			name: "mixed section modes are unknown",
			blocks: []json.RawMessage{
				rawBlock(t, map[string]interface{}{"type": "section", "expand": true}),
				rawBlock(t, map[string]interface{}{"type": "section"}),
			},
		},
		{
			name: "mixed native and section layout is unknown",
			blocks: []json.RawMessage{
				rawBlock(t, map[string]interface{}{"type": "rich_text"}),
				rawBlock(t, map[string]interface{}{"type": "section", "expand": true}),
			},
		},
		{
			name:   "custom block is unknown",
			blocks: []json.RawMessage{rawBlock(t, map[string]interface{}{"type": "actions"})},
		},
		{name: "invalid block is unknown", blocks: []json.RawMessage{json.RawMessage(`{`)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, known := DetectBlocks(test.blocks)
			if got != test.want || known != test.known {
				t.Fatalf("DetectBlocks() = %q, %t; want %q, %t", got, known, test.want, test.known)
			}
		})
	}
}
