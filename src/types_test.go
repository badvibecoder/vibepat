package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMatchResultAlwaysEmitsSameKeys is the schema gate: every documented key
// must appear even for a zero-valued match, so JSON consumers never have to
// probe for key presence.
func TestMatchResultAlwaysEmitsSameKeys(t *testing.T) {
	blob, err := json.Marshal(MatchResult{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := []string{
		"matched_tokens",
		"context_lines",
		"stanza_index",
		"line_number",
		"position_kind",
		"stanza",
		"mutations",
	}
	for _, key := range want {
		if _, ok := raw[key]; !ok {
			t.Errorf("key %q missing from serialized MatchResult: %s", key, blob)
		}
	}
	if len(raw) != len(want) {
		t.Errorf("serialized MatchResult has %d keys, want %d: %s", len(raw), len(want), blob)
	}
}

// TestNewMatchResultAllocatesCollections verifies the constructor pre-allocates
// map and slice fields so they serialize as {} and [] rather than null.
func TestNewMatchResultAllocatesCollections(t *testing.T) {
	blob, err := json.Marshal(NewMatchResult(PositionLine, 10, nil))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	s := string(blob)
	if strings.Contains(s, `"matched_tokens":null`) {
		t.Errorf("matched_tokens serialized as null: %s", s)
	}
	// The value must always be an array, even when empty or single-valued.
	if !strings.Contains(s, `"matched_tokens":{}`) {
		t.Errorf("empty matched_tokens is not an object: %s", s)
	}
	if strings.Contains(s, `"context_lines":null`) {
		t.Errorf("context_lines serialized as null: %s", s)
	}
}

// TestNewMatchResultCopiesContext verifies the constructor does not alias the
// caller's slice, so a ring buffer's backing array can be reused safely.
func TestNewMatchResultCopiesContext(t *testing.T) {
	ctx := []string{"a", "b"}
	result := NewMatchResult(PositionLine, 5, ctx)

	ctx[0] = "MUTATED"

	if result.ContextLines[0] != "a" {
		t.Fatalf("ContextLines aliased caller slice: got %q, want %q", result.ContextLines[0], "a")
	}
}
