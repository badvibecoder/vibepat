// Package main implements vibepat, a zero-friction tool for slicing, filtering,
// and rewriting log, config, and hardware-topology text streams.
//
// This file holds the foundational data structures shared by every phase of the
// build. It deliberately contains no matching, parsing, or I/O logic.
package main

// Boundary constants describe how a Stanza was delimited. They are populated by
// the Phase 2 visual scanner; Phase 1 leaves them empty except where noted.
const (
	// BoundaryStream marks a stanza that is exactly one line.
	BoundaryStream = "stream"
	// BoundaryHeader marks a stanza anchored on a header line (INI sections,
	// lspci BDF headers).
	BoundaryHeader = "header"
	// BoundaryIndent marks a stanza anchored on an indentation ridge (YAML,
	// `ip a` output).
	BoundaryIndent = "indent"
)

// Position kinds discriminate which positional field of a MatchResult is
// meaningful. Consumers switch on this rather than guessing from zero values.
const (
	// PositionLine means LineNumber is populated and StanzaIndex is 0.
	PositionLine = "line"
	// PositionStanza means StanzaIndex is populated and LineNumber is the
	// 1-based line on which the stanza began.
	PositionStanza = "stanza"
)

// Canonical semantic token names. The Phase 4 registry implements the matching
// for these; naming them here keeps call sites free of typos.
const (
	TokenIP            = "ip"
	TokenMAC           = "mac"
	TokenBDF           = "bdf"
	TokenNUMA          = "numa"
	TokenError         = "error"
	TokenLinkDowngrade = "link_downgrade"
)

// Stanza is a logical chunk of text. Phase 2 populates every field; Phase 1 uses
// only Lines.
type Stanza struct {
	// Lines holds the stanza's lines with any trailing newline stripped. The
	// slice aliases no scanner buffer and is safe to retain.
	Lines []string `json:"lines"`
	// RawText is Lines rejoined with "\n". It is the single string that Phase 4
	// token matchers are run against.
	RawText string `json:"raw_text"`
	// BoundaryType records which visual heuristic delimited the stanza: one of
	// BoundaryStream, BoundaryHeader, or BoundaryIndent.
	BoundaryType string `json:"boundary_type"`
}

// MatchResult is one entry in vibepat's JSON output array.
//
// Every field is always serialized so that consumers -- jq filters, CI scripts,
// LLMs -- never have to probe for key presence. Fields that do not apply to a
// given match carry their zero value ("" , 0, or null) rather than being
// omitted. Use PositionKind to decide which positional field is authoritative.
type MatchResult struct {
	// MatchedTokens maps canonical semantic token names (see the Token*
	// constants) to every distinct value found for that token, in order of
	// appearance. Tokens with no matches are absent rather than present-and-empty.
	//
	// The value is always an array, even for a single match, so consumers never
	// have to handle two shapes. Nothing is joined or truncated: a stanza with
	// four IPs reports all four.
	//
	// Always non-nil in output so it serializes as {} rather than null.
	MatchedTokens map[string][]string `json:"matched_tokens"`
	// ContextLines holds up to N lines that preceded the match, in
	// chronological order (oldest first). Always non-nil in output so it
	// serializes as [] rather than null.
	ContextLines []string `json:"context_lines"`
	// StanzaIndex is the 1-based index of the matched stanza. Zero when
	// PositionKind is PositionLine.
	StanzaIndex int `json:"stanza_index"`
	// LineNumber is the 1-based input line the match was found on. For stanza
	// matches this is the line on which the stanza began.
	LineNumber int `json:"line_number"`
	// PositionKind is either PositionLine or PositionStanza.
	PositionKind string `json:"position_kind"`
	// Stanza carries the full matched stanza. Nil for line-oriented matches.
	Stanza *Stanza `json:"stanza"`
	// Mutations records the changes applied to this stanza. Nil until the Phase
	// 3 mutation tracker populates it.
	Mutations []Mutation `json:"mutations"`
}

// NewMatchResult returns a MatchResult with the map and slice fields allocated,
// so it always serializes with stable JSON types.
func NewMatchResult(positionKind string, position int, context []string) MatchResult {
	lines := make([]string, len(context))
	copy(lines, context)
	return MatchResult{
		MatchedTokens: map[string][]string{},
		ContextLines:  lines,
		LineNumber:    position,
		PositionKind:  positionKind,
	}
}
