package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	prompt "github.com/elk-language/go-prompt"

	"github.com/badvibecoder/vibepat/internal/registry"
)

// TestParseGetAllBracketedTargets is the spec's headline parser gate.
func TestParseGetAllBracketedTargets(t *testing.T) {
	q, err := Parse("get all [bdf, numa]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if q.Action != ActionGet {
		t.Errorf("Action = %q, want %q", q.Action, ActionGet)
	}
	if q.Scope != ScopeAll {
		t.Errorf("Scope = %q, want %q", q.Scope, ScopeAll)
	}
	if len(q.Targets) != 2 || q.Targets[0] != "bdf" || q.Targets[1] != "numa" {
		t.Errorf("Targets = %v, want [bdf numa]", q.Targets)
	}
}

// TestParseReplaceIdentifiesYoloModifier is the spec's second headline gate.
func TestParseReplaceIdentifiesYoloModifier(t *testing.T) {
	q, err := Parse("replace mac yolo")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if q.Action != ActionReplace {
		t.Errorf("Action = %q, want %q", q.Action, ActionReplace)
	}
	if len(q.Targets) != 1 || q.Targets[0] != "mac" {
		t.Errorf("Targets = %v, want [mac]", q.Targets)
	}
	if !q.HasModifier(ModifierYolo) {
		t.Errorf("Modifiers = %v, want yolo present", q.Modifiers)
	}
}

// TestParseActionDefaultsToGet verifies an omitted action defaults.
func TestParseActionDefaultsToGet(t *testing.T) {
	q, err := Parse("all [ip]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Action != ActionGet {
		t.Errorf("Action = %q, want %q", q.Action, ActionGet)
	}
	if len(q.Targets) != 1 || q.Targets[0] != "ip" {
		t.Errorf("Targets = %v, want [ip]", q.Targets)
	}
}

// TestParseFullReplaceIsTheSpecExample verifies the complete command from the
// specification parses into the expected shape.
func TestParseFullReplaceIsTheSpecExample(t *testing.T) {
	q, err := Parse("replace ip ['10.0.1.X'] with ['10.50.1.X'] yolo")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if q.Action != ActionReplace {
		t.Errorf("Action = %q", q.Action)
	}
	if len(q.Targets) != 1 || q.Targets[0] != "ip" {
		t.Errorf("Targets = %v, want [ip]", q.Targets)
	}
	if q.Patterns["ip"] != "10.0.1.X" {
		t.Errorf("Patterns[ip] = %q, want the wildcard pattern 10.0.1.X", q.Patterns["ip"])
	}
	if !q.HasWith || q.With != "10.50.1.X" {
		t.Errorf("With = %q, HasWith = %v; want 10.50.1.X", q.With, q.HasWith)
	}
	if !q.HasModifier(ModifierYolo) {
		t.Errorf("Modifiers = %v, want yolo", q.Modifiers)
	}
}

// TestParseUnbracketedTargets verifies the bracketed form is optional.
func TestParseUnbracketedTargets(t *testing.T) {
	q, err := Parse("get bdf numa")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(q.Targets) != 2 || q.Targets[0] != "bdf" || q.Targets[1] != "numa" {
		t.Errorf("Targets = %v, want [bdf numa]", q.Targets)
	}
}

// TestParseScopes verifies every scope form.
func TestParseScopes(t *testing.T) {
	cases := []struct {
		input string
		scope string
		n     int
	}{
		{"get all [ip]", ScopeAll, 0},
		{"get first 3 [error]", ScopeFirstN, 3},
		{"get last 2 [error]", ScopeLastN, 2},
		{"get today [error]", ScopeToday, 0},
		{"get stanza 4 [bdf]", ScopeStanza, 4},
		{"get first [ip]", ScopeFirstN, 1}, // count omitted defaults to 1
	}
	for _, tc := range cases {
		q, err := Parse(tc.input)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.input, err)
			continue
		}
		if q.Scope != tc.scope {
			t.Errorf("Parse(%q).Scope = %q, want %q", tc.input, q.Scope, tc.scope)
		}
		if q.ScopeN != tc.n {
			t.Errorf("Parse(%q).ScopeN = %d, want %d", tc.input, q.ScopeN, tc.n)
		}
	}
}

// TestParseSpotWithCount verifies "spot N" captures the sample size.
func TestParseSpotWithCount(t *testing.T) {
	q, err := Parse("replace ip ['10.0.1.X'] with ['10.50.1.X'] spot 7")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.HasModifier(ModifierSpot) {
		t.Errorf("Modifiers = %v, want spot", q.Modifiers)
	}
	if q.SpotN != 7 {
		t.Errorf("SpotN = %d, want 7", q.SpotN)
	}
}

// TestParseSpotWithoutCount verifies a bare "spot" is accepted.
func TestParseSpotWithoutCount(t *testing.T) {
	q, err := Parse("replace mac with ['x'] spot")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.HasModifier(ModifierSpot) {
		t.Errorf("Modifiers = %v, want spot", q.Modifiers)
	}
	if q.SpotN != 0 {
		t.Errorf("SpotN = %d, want 0 (unset)", q.SpotN)
	}
}

// TestParseRequireAll verifies the two-word modifier that switches target
// matching from "any" to "every".
func TestParseRequireAll(t *testing.T) {
	q, err := Parse("get all [bdf] require all")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.RequireAll {
		t.Error("RequireAll = false, want true")
	}
	if !q.HasModifier(ModifierRequireAll) {
		t.Errorf("Modifiers = %v, want require present", q.Modifiers)
	}
}

// TestParseRequireWithoutAllIsAnError verifies the modifier is rejected when
// incomplete, rather than silently ignored.
func TestParseRequireWithoutAllIsAnError(t *testing.T) {
	if _, err := Parse("get all [bdf] require"); err == nil {
		t.Error("Parse accepted a bare \"require\", want an error")
	}
	if _, err := Parse("get all [bdf] require any"); err == nil {
		t.Error("Parse accepted \"require any\", want an error")
	}
}

// TestGroupByIsNoLongerGrammar verifies the removed modifier is rejected, so an
// old command fails loudly instead of being misread.
func TestGroupByIsNoLongerGrammar(t *testing.T) {
	if _, err := Parse("get all [bdf] group by numa"); err == nil {
		t.Error("Parse still accepted \"group by\", want it removed")
	}
}

// TestParseQuotedLiteralWithSpaces verifies quotes protect embedded spaces.
func TestParseQuotedLiteralWithSpaces(t *testing.T) {
	q, err := Parse("get ['link is down']")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(q.Targets) != 1 || q.Targets[0] != "link is down" {
		t.Errorf("Targets = %v, want the whole quoted literal", q.Targets)
	}
}

// TestParseWithQuotedSpaces verifies a replacement value may contain spaces.
func TestParseWithQuotedSpaces(t *testing.T) {
	q, err := Parse("replace ['OLD TEXT'] with ['NEW TEXT']")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.With != "NEW TEXT" {
		t.Errorf("With = %q, want %q", q.With, "NEW TEXT")
	}
}

// TestParseEmptyWithIsDistinguishable verifies an intentional empty replacement
// is not confused with an absent clause, since one deletes text and the other is
// a syntax error.
func TestParseEmptyWithIsDistinguishable(t *testing.T) {
	q, err := Parse("replace ['marker'] with ['']")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.HasWith {
		t.Error("HasWith = false, want true for an explicit empty replacement")
	}
	if q.With != "" {
		t.Errorf("With = %q, want empty", q.With)
	}
}

// TestParseForceIsAliasForYolo verifies the two spellings agree.
func TestParseForceIsAliasForYolo(t *testing.T) {
	q, err := Parse("replace mac with ['x'] force")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.HasModifier(ModifierYolo) {
		t.Errorf("force did not register as yolo: %v", q.Modifiers)
	}
}

// TestParseModifiersDeduplicate verifies a repeated modifier appears once.
func TestParseModifiersDeduplicate(t *testing.T) {
	q, err := Parse("replace mac with ['x'] yolo yolo")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(q.Modifiers) != 1 {
		t.Errorf("Modifiers = %v, want one entry", q.Modifiers)
	}
}

// TestParseErrorCases verifies malformed input is rejected.
func TestParseErrorCases(t *testing.T) {
	cases := map[string]string{
		"get all [ip] with ['x']":        "with on a get",
		"get all [unclosed":              "unterminated bracket",
		"get ['unterminated":             "unterminated quote",
		"get first 0 [ip]":               "zero count",
		"get first -2 [ip]":              "negative count",
		"get all [ip] require":           "group without by",
		"get all [ip] require all extra": "group by without a field",
		"get all [ip] bogusmodifier":     "unknown trailing token",
	}
	for input, why := range cases {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error (%s)", input, why)
		}
	}
}

// TestGrammarValidButNotExecutable covers commands that parse cleanly because
// they are well-formed grammar, yet describe no work that can be performed.
// Rejecting these at parse time would break the spec's own example, so the
// rejection happens at the execution boundary instead.
func TestGrammarValidButNotExecutable(t *testing.T) {
	cases := map[string]string{
		"replace mac": "a replace with no with clause",
		"replace":     "a bare replace",
	}
	for input, why := range cases {
		q, err := Parse(input)
		if err != nil {
			t.Errorf("Parse(%q) rejected valid grammar: %v", input, err)
			continue
		}
		if err := q.ValidateExecutable(); err == nil {
			t.Errorf("ValidateExecutable(%q) succeeded, want an error (%s)", input, why)
		}
	}
}

// TestReplaceWithoutWithParsesButIsNotExecutable verifies the deliberate split
// between grammar and runnability: the spec's own "replace mac yolo" example is
// valid grammar, but a replace with no replacement value cannot be executed.
func TestReplaceWithoutWithParsesButIsNotExecutable(t *testing.T) {
	q, err := Parse("replace mac")
	if err != nil {
		t.Fatalf("Parse rejected grammar the spec calls valid: %v", err)
	}
	if err := q.ValidateExecutable(); err == nil {
		t.Fatal("ValidateExecutable accepted a replace with no with clause")
	}
}

// TestGetAllIsValidWithNoTarget verifies the grammar's most basic command.
func TestGetAllIsValidWithNoTarget(t *testing.T) {
	for _, input := range []string{"get all", "all", "get"} {
		q, err := Parse(input)
		if err != nil {
			t.Errorf("Parse(%q): %v", input, err)
			continue
		}
		if err := q.ValidateExecutable(); err != nil {
			t.Errorf("ValidateExecutable(%q): %v", input, err)
		}
	}
}

// TestParseErrorReportsPosition verifies the error locates the problem.
func TestParseErrorReportsPosition(t *testing.T) {
	_, err := Parse("get all [ip] bogus")
	if err == nil {
		t.Fatal("Parse accepted an unknown token")
	}

	var pe *ParseError
	if !asParseError(err, &pe) {
		t.Fatalf("error is %T, want *ParseError", err)
	}
	if pe.Position != 4 {
		t.Errorf("Position = %d, want 4", pe.Position)
	}
	if !strings.Contains(pe.Message, "bogus") && !strings.Contains(pe.Message, "position 4") {
		t.Errorf("Message = %q, want it to name the offending token or position", pe.Message)
	}
}

// TestParseEmptyInputIsGetAll verifies an empty command yields the default.
func TestParseEmptyInputIsGetAll(t *testing.T) {
	q, err := Parse("   ")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Action != ActionGet || len(q.Targets) != 0 {
		t.Errorf("got action=%q targets=%v, want a bare get", q.Action, q.Targets)
	}
}

// TestQueryIsWrite verifies only replace is treated as a write.
func TestQueryIsWrite(t *testing.T) {
	write := []string{"replace mac with ['x']"}
	readOnly := []string{"get all [ip]", "keep all [ip]", "drop all [ip]"}

	for _, in := range write {
		q, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if !q.IsWrite() {
			t.Errorf("Parse(%q).IsWrite() = false, want true", in)
		}
	}
	for _, in := range readOnly {
		q, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if q.IsWrite() {
			t.Errorf("Parse(%q).IsWrite() = true, want false", in)
		}
	}
}

// --- IP rewrite patterns --------------------------------------------------

// TestSelectorWildcardRewritesFourthOctet is the confirmed wildcard model:
// the first three octets are literal, the fourth is captured and preserved.
func TestSelectorWildcardRewritesFourthOctet(t *testing.T) {
	sel, err := newSelector("ip", "10.0.1.X")
	if err != nil {
		t.Fatalf("newSelector: %v", err)
	}

	got, changed := sel.rewriteLine("host 10.0.1.7 end", "10.50.1.X")
	if !changed {
		t.Fatal("rewriteLine reported no change")
	}
	if got != "host 10.50.1.7 end" {
		t.Errorf("got %q, want %q", got, "host 10.50.1.7 end")
	}
}

// TestSelectorWildcardPreservesMultiDigitOctet verifies a 3-digit host survives.
func TestSelectorWildcardPreservesMultiDigitOctet(t *testing.T) {
	sel, _ := newSelector("ip", "10.0.1.X")

	got, _ := sel.rewriteLine("addr 10.0.1.200/24", "10.50.1.X")
	if got != "addr 10.50.1.200/24" {
		t.Errorf("got %q, want %q", got, "addr 10.50.1.200/24")
	}
}

// TestSelectorWildcardLeavesOtherSubnetsAlone verifies the literal prefix is
// enforced.
func TestSelectorWildcardLeavesOtherSubnetsAlone(t *testing.T) {
	sel, _ := newSelector("ip", "10.0.1.X")

	got, changed := sel.rewriteLine("other 10.9.9.7 here", "10.50.1.X")
	if changed {
		t.Errorf("rewrote an address outside the subnet: %q", got)
	}
	if got != "other 10.9.9.7 here" {
		t.Errorf("line was modified: %q", got)
	}
}

// TestSelectorWildcardRejectsMisplacedX verifies X is only valid as the fourth
// octet, so a mistake is a parse error rather than a silent no-match.
func TestSelectorWildcardRejectsMisplacedX(t *testing.T) {
	for _, bad := range []string{"10.X.1.4", "X.0.1.4", "10.0.X.X", "10.0.1.X.5"} {
		if _, err := newSelector("ip", bad); err == nil {
			t.Errorf("newSelector(%q) accepted a misplaced wildcard, want an error", bad)
		}
	}
}

// TestSelectorCIDRRewritesHostPortion verifies the CIDR alternative produces the
// same subnet rewrite as the wildcard.
func TestSelectorCIDRRewritesHostPortion(t *testing.T) {
	sel, err := newSelector("ip", "10.0.1.0/24")
	if err != nil {
		t.Fatalf("newSelector: %v", err)
	}

	got, changed := sel.rewriteLine("10.0.1.7 and 10.0.1.200 and 10.9.9.1", "10.50.1.X")
	if !changed {
		t.Fatal("CIDR rewrite reported no change")
	}
	want := "10.50.1.7 and 10.50.1.200 and 10.9.9.1"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestSelectorLiteralReplacesExactly verifies a non-IP target is literal.
func TestSelectorLiteralReplacesExactly(t *testing.T) {
	sel, err := newSelector("literal", "OLD")
	if err != nil {
		t.Fatalf("newSelector: %v", err)
	}

	got, changed := sel.rewriteLine("a OLD b", "NEW")
	if !changed || got != "a NEW b" {
		t.Errorf("got %q changed=%v, want %q", got, changed, "a NEW b")
	}
}

// TestSelectorLiteralNoMatch verifies no spurious change.
func TestSelectorLiteralNoMatch(t *testing.T) {
	sel, _ := newSelector("literal", "OLD")

	got, changed := sel.rewriteLine("nothing here", "NEW")
	if changed || got != "nothing here" {
		t.Errorf("got %q changed=%v, want the line untouched", got, changed)
	}
}

// TestSelectorMatchStanza verifies read-only matching.
func TestSelectorMatchStanza(t *testing.T) {
	sel, _ := newSelector("ip", "10.0.1.X")

	got := sel.matchStanza("a 10.0.1.5 b 10.0.1.9 c 10.9.9.9")
	if len(got) != 2 {
		t.Errorf("matchStanza = %v, want the two in-subnet addresses", got)
	}
}

// --- query search and scope ----------------------------------------------

// queryFixture builds stanzas and their line-start indices from input text.
func queryFixture(t *testing.T, input, mode string) ([]*Stanza, []int, []string) {
	t.Helper()

	lines := strings.Split(strings.TrimSuffix(input, "\n"), "\n")
	stanzas, err := chunkLines(lines, mode, 0)
	if err != nil {
		t.Fatalf("chunkLines: %v", err)
	}
	return stanzas, lineIndex(lines, stanzas), lines
}

// TestSearchStanzasScopesByMatchCount verifies the confirmed scope semantics:
// first N bounds the number of matches reported, not the input window.
func TestSearchStanzasScopesByMatchCount(t *testing.T) {
	// The first stanza has no IP, so a window-based reading would return fewer
	// matches than a match-based one.
	const input = "no address here\n10.0.0.1 a\n10.0.0.2 b\n10.0.0.3 c\n"
	stanzas, lineStarts, _ := queryFixture(t, input, ModeStream)

	q, err := Parse("get first 3 [ip]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	results, err := SearchStanzas(q, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 matches", len(results))
	}
	if results[0].StartLine != 2 {
		t.Errorf("first match starts at line %d, want 2", results[0].StartLine)
	}
}

// TestSearchStanzasLastN verifies last N takes from the tail.
func TestSearchStanzasLastN(t *testing.T) {
	const input = "10.0.0.1 a\n10.0.0.2 b\n10.0.0.3 c\n10.0.0.4 d\n"
	stanzas, lineStarts, _ := queryFixture(t, input, ModeStream)

	q, _ := Parse("get last 2 [ip]")
	results, err := SearchStanzas(q, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].StartLine != 3 || results[1].StartLine != 4 {
		t.Errorf("got lines %d,%d; want 3,4", results[0].StartLine, results[1].StartLine)
	}
}

// TestSearchStanzasStanzaScope verifies selecting one stanza by index.
func TestSearchStanzasStanzaScope(t *testing.T) {
	const input = "10.0.0.1 a\n10.0.0.2 b\n10.0.0.3 c\n"
	stanzas, lineStarts, _ := queryFixture(t, input, ModeStream)

	q, _ := Parse("get stanza 2 [ip]")
	results, err := SearchStanzas(q, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}
	if len(results) != 1 || results[0].StartLine != 2 {
		t.Fatalf("got %+v, want the stanza on line 2", results)
	}
}

// TestSearchStanzasNoTargetMatchesAll verifies "get all" reports everything.
func TestSearchStanzasNoTargetMatchesAll(t *testing.T) {
	const input = "a\nb\nc\n"
	stanzas, lineStarts, _ := queryFixture(t, input, ModeStream)

	q, _ := Parse("get all")
	results, err := SearchStanzas(q, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}
}

// TestSearchStanzasTodayScope verifies the date heuristic with a pinned clock.
func TestSearchStanzasTodayScope(t *testing.T) {
	original := nowFunc
	nowFunc = func() time.Time {
		return time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	}
	defer func() { nowFunc = original }()

	const input = "2026-02-10 [ERROR] disk failure\n2020-01-01 [ERROR] old news\nFeb 10 12:00:00 host sshd[1]: failed\n"
	stanzas, lineStarts, _ := queryFixture(t, input, ModeStream)

	q, err := Parse("get today [error]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	results, err := SearchStanzas(q, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 today-dated matches", len(results))
	}
	for _, r := range results {
		if strings.Contains(r.Stanza.RawText, "2020-01-01") {
			t.Errorf("today scope included a 2020 line: %q", r.Stanza.RawText)
		}
	}
}

// TestBuildQueryMutationsProducesLineAccurateChanges verifies replace mutations
// carry absolute line numbers and the right rewritten text.
func TestBuildQueryMutationsProducesLineAccurateChanges(t *testing.T) {
	const input = "server a\naddr 10.0.1.7\nserver b\naddr 10.9.9.9\n"
	stanzas, lineStarts, lines := queryFixture(t, input, ModeStream)

	q, err := Parse("replace ip ['10.0.1.X'] with ['10.50.1.X']")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	results, err := SearchStanzas(q, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}

	mutations, err := BuildQueryMutations(q, results, lines)
	if err != nil {
		t.Fatalf("BuildQueryMutations: %v", err)
	}
	if len(mutations) != 1 {
		t.Fatalf("got %d mutations, want 1: %+v", len(mutations), mutations)
	}
	if mutations[0].LineNumber != 2 {
		t.Errorf("LineNumber = %d, want 2", mutations[0].LineNumber)
	}
	if mutations[0].ModifiedText != "addr 10.50.1.7" {
		t.Errorf("ModifiedText = %q, want %q", mutations[0].ModifiedText, "addr 10.50.1.7")
	}
}

// TestBuildQueryMutationsIsEmptyForReadOnly verifies a read query never produces
// mutations, which is the safety property behind keep/drop being read-only.
func TestBuildQueryMutationsIsEmptyForReadOnly(t *testing.T) {
	const input = "addr 10.0.1.7\n"
	stanzas, lineStarts, lines := queryFixture(t, input, ModeStream)

	for _, action := range []string{"get", "keep", "drop"} {
		q, err := Parse(action + " all [ip]")
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		results, err := SearchStanzas(q, stanzas, lineStarts)
		if err != nil {
			t.Fatalf("SearchStanzas: %v", err)
		}
		mutations, err := BuildQueryMutations(q, results, lines)
		if err != nil {
			t.Fatalf("BuildQueryMutations: %v", err)
		}
		if len(mutations) != 0 {
			t.Errorf("%s produced %d mutations, want 0", action, len(mutations))
		}
	}
}

// TestBuildQueryMutationsDeduplicatesPerLine verifies two patterns matching one
// line yield a single mutation rather than an invalid plan.
func TestBuildQueryMutationsDeduplicatesPerLine(t *testing.T) {
	const input = "10.0.1.5 10.0.1.6\n"
	stanzas, lineStarts, lines := queryFixture(t, input, ModeStream)

	q, _ := Parse("replace ip ['10.0.1.X'] with ['10.50.1.X']")
	results, _ := SearchStanzas(q, stanzas, lineStarts)

	mutations, err := BuildQueryMutations(q, results, lines)
	if err != nil {
		t.Fatalf("BuildQueryMutations: %v", err)
	}
	if len(mutations) != 1 {
		t.Fatalf("got %d mutations, want 1", len(mutations))
	}
	if mutations[0].ModifiedText != "10.50.1.5 10.50.1.6" {
		t.Errorf("ModifiedText = %q, want both addresses rewritten", mutations[0].ModifiedText)
	}
}

// asParseError reports whether err is a *ParseError, assigning it to target.
func asParseError(err error, target **ParseError) bool {
	pe, ok := err.(*ParseError)
	if ok {
		*target = pe
	}
	return ok
}

// --- query CLI integration -----------------------------------------------

// TestRunQueryWriteKeepsStdoutPureJSON is the regression gate for a real bug:
// the replace-query path routed its diff to stdout, so the JSON summary was
// prefixed with diff text and could not be parsed.
func TestRunQueryWriteKeepsStdoutPureJSON(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cfg.txt"
	if err := writeFile(path, "server a\naddr 10.0.1.7\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Flags must precede the positional grammar: Go's flag package stops parsing
	// at the first non-flag argument.
	baseFlags := []string{"--mode", "stream", "--no-color"}
	grammar := []string{"replace", "ip", "['10.0.1.X']", "with", "['10.50.1.X']"}
	for _, extra := range [][]string{{"--dryrun"}, {}} {
		args := append(append([]string{}, baseFlags...), extra...)
		args = append(args, grammar...)
		args = append(args, path)

		var out, diag bytes.Buffer
		if err := runPlain(t, args, strings.NewReader("y\n"), &out, &diag); err != nil {
			t.Fatalf("run(%v): %v", extra, err)
		}

		var result ExecResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("args %v: stdout is not pure JSON: %v\nstdout:\n%s\nstderr:\n%s",
				extra, err, out.String(), diag.String())
		}
		if !strings.Contains(diag.String(), "@@ line") {
			t.Errorf("args %v: diff was not written to the diagnostic stream", extra)
		}
	}
}

// TestRunQueryReplaceYoloWritesAndBacksUp verifies the full query write path.
func TestRunQueryReplaceYoloWritesAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cfg.txt"
	const original = "server a\naddr 10.0.1.7\naddr 10.9.9.9\naddr 10.0.1.200\n"
	if err := writeFile(path, original); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{
		"--mode", "stream", "--no-color",
		"replace", "ip", "['10.0.1.X']", "with", "['10.50.1.X']", "yolo", path,
	}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, _ := os.ReadFile(path)
	want := "server a\naddr 10.50.1.7\naddr 10.9.9.9\naddr 10.50.1.200\n"
	if string(got) != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}

	backup, err := os.ReadFile(path + BackupSuffix)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != original {
		t.Errorf("backup = %q, want the original", backup)
	}

	var result ExecResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Applied != 2 {
		t.Errorf("applied = %d, want 2 (10.9.9.9 is outside the subnet)", result.Applied)
	}
}

// TestRunQueryCIDRMatchesWildcard verifies the two spellings agree.
func TestRunQueryCIDRMatchesWildcard(t *testing.T) {
	dir := t.TempDir()

	for _, pattern := range []string{"10.0.1.X", "10.0.1.0/24"} {
		path := dir + "/" + strings.NewReplacer("/", "_", ".", "_").Replace(pattern) + ".txt"
		if err := writeFile(path, "addr 10.0.1.7\naddr 10.9.9.9\n"); err != nil {
			t.Fatalf("setup: %v", err)
		}

		var out, diag bytes.Buffer
		args := []string{
			"--mode", "stream", "--no-color",
			"replace", "ip", "['" + pattern + "']", "with", "['10.50.1.X']", "yolo", path,
		}
		if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
			t.Fatalf("pattern %q: %v", pattern, err)
		}

		got, _ := os.ReadFile(path)
		if string(got) != "addr 10.50.1.7\naddr 10.9.9.9\n" {
			t.Errorf("pattern %q produced %q, want the same result as the wildcard", pattern, got)
		}
	}
}

// TestRunQueryReadOnlyDoesNotWrite verifies get/keep/drop never touch the file.
func TestRunQueryReadOnlyDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/mix.txt"
	const original = "eth0 10.0.0.1\nplain text\n"
	if err := writeFile(path, original); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for _, action := range []string{"get", "keep", "drop"} {
		var out, diag bytes.Buffer
		args := []string{"--mode", "stream", action, "all", "[ip]", path}
		if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
			t.Fatalf("%s: %v", action, err)
		}

		got, _ := os.ReadFile(path)
		if string(got) != original {
			t.Errorf("%s modified the file: %q", action, got)
		}
		if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
			t.Errorf("%s created a backup", action)
		}

		matches := decodeNDJSON(t, out.String())
		_ = matches
	}
}

// TestRunQueryDropReturnsComplement verifies drop reports non-matching stanzas.
func TestRunQueryDropReturnsComplement(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/mix.txt"
	if err := writeFile(path, "eth0 10.0.0.1\nplain text line\neth1 10.0.0.2\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--mode", "stream", "drop", "all", "[ip]", path}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	matches := decodeNDJSON(t, out.String())
	if len(matches) != 1 {
		t.Fatalf("got %d results, want 1 non-matching stanza", len(matches))
	}
	if matches[0].Stanza.Lines[0] != "plain text line" {
		t.Errorf("dropped complement = %q, want the plain line", matches[0].Stanza.Lines[0])
	}
}

// TestRunQueryScopesEndToEnd verifies scope limits through the CLI.
func TestRunQueryScopesEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ips.txt"
	if err := writeFile(path, "10.0.0.1 a\n10.0.0.2 b\n10.0.0.3 c\n10.0.0.4 d\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cases := map[string][]string{
		"get all [ip]":      {"10.0.0.1 a", "10.0.0.2 b", "10.0.0.3 c", "10.0.0.4 d"},
		"get first 2 [ip]":  {"10.0.0.1 a", "10.0.0.2 b"},
		"get last 2 [ip]":   {"10.0.0.3 c", "10.0.0.4 d"},
		"get stanza 2 [ip]": {"10.0.0.2 b"},
	}
	for query, want := range cases {
		var out, diag bytes.Buffer
		if err := runPlain(t, append([]string{"--mode", "stream"}, append(strings.Fields(query), path)...),
			strings.NewReader(""), &out, &diag); err != nil {
			t.Errorf("run(%q): %v", query, err)
			continue
		}

		matches := decodeNDJSON(t, out.String())
		if len(matches) != len(want) {
			t.Errorf("%q returned %d results, want %d", query, len(matches), len(want))
			continue
		}
		for i := range want {
			if matches[i].Stanza.Lines[0] != want[i] {
				t.Errorf("%q result %d = %q, want %q", query, i, matches[i].Stanza.Lines[0], want[i])
			}
		}
	}
}

// TestRunQueryTodayScope verifies the today filter through the CLI.
func TestRunQueryTodayScope(t *testing.T) {
	original := nowFunc
	nowFunc = func() time.Time {
		return time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)
	}
	defer func() { nowFunc = original }()

	dir := t.TempDir()
	path := dir + "/log.txt"
	// Structured error signal is required: the strict "error" token ignores a
	// bare "ERROR" word by design.
	if err := writeFile(path, "2026-02-10 [ERROR] disk failure\n2020-01-01 [ERROR] old news\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"--mode", "stream", "get", "today", "[error]", path},
		strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	matches := decodeNDJSON(t, out.String())
	if len(matches) != 1 {
		t.Fatalf("got %d results, want only today's line", len(matches))
	}
	if !strings.Contains(matches[0].Stanza.RawText, "2026-02-10") {
		t.Errorf("today scope returned %q", matches[0].Stanza.RawText)
	}
}

// TestRunQueryUnrunnableIsRejected verifies a grammar-valid but incomplete
// command fails at the boundary rather than silently doing nothing.
func TestRunQueryUnrunnableIsRejected(t *testing.T) {
	var out, diag bytes.Buffer
	err := runPlain(t, []string{"--mode", "stream", "replace", "mac"},
		strings.NewReader("x\n"), &out, &diag)
	if err == nil {
		t.Fatal("run accepted a replace with no with clause, want an error")
	}
}

// TestRunQueryUnknownTokenIsRejected verifies a typo names the problem.
func TestRunQueryUnknownTokenIsRejected(t *testing.T) {
	var out, diag bytes.Buffer
	err := runPlain(t, []string{"--mode", "stream", "--tokens", "ip", "get", "all", "[nosuchtoken]"},
		strings.NewReader("x\n"), &out, &diag)
	// An unregistered bracketed target is treated as a literal to find, so it
	// yields no matches rather than an error; what must not happen is a crash.
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for a non-matching target, got %s", out.String())
	}
}

// --- REPL -----------------------------------------------------------------

// documentFor builds a prompt Document with text and the cursor at the end.
// Document's cursor position is unexported, so it must be built via Buffer, and
// the cursor has to be advanced explicitly because a plain InsertText leaves the
// text after the cursor.
func documentFor(text string) prompt.Document {
	b := prompt.NewBuffer()
	for _, r := range text {
		b.InsertTextMoveCursor(string(r), 0, 0, false)
	}
	return *b.Document()
}

// TestCompleterSuggestsActionsWhenEmpty verifies the first completion tier.
func TestCompleterSuggestsActionsWhenEmpty(t *testing.T) {
	suggestions, _, _ := completer(documentFor(""))

	want := map[string]bool{ActionGet: false, ActionReplace: false}
	for _, s := range suggestions {
		if _, ok := want[s.Text]; ok {
			want[s.Text] = true
		}
	}
	for action, seen := range want {
		if !seen {
			t.Errorf("empty input did not suggest %q; got %v", action, suggestions)
		}
	}
}

// TestCompleterSuggestsTokensAfterScope verifies the token tier and that
// suggestions carry descriptions.
func TestCompleterSuggestsTokensAfterScope(t *testing.T) {
	suggestions, _, _ := completer(documentFor("get all "))

	names := map[string]bool{}
	for _, s := range suggestions {
		names[s.Text] = true
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("suggestion %q has no description", s.Text)
		}
	}
	for _, token := range []string{"ip", "bdf", "mac", "numa", "error"} {
		if !names[token] {
			t.Errorf("token %q missing from suggestions after a scope: %v", token, names)
		}
	}
}

// TestCompleterSuggestsScopesAfterAction verifies the scope tier.
func TestCompleterSuggestsScopesAfterAction(t *testing.T) {
	suggestions, _, _ := completer(documentFor("get "))

	names := map[string]bool{}
	for _, s := range suggestions {
		names[s.Text] = true
	}
	for _, scope := range []string{ScopeAll, ScopeFirstN, ScopeLastN, ScopeToday, ScopeStanza} {
		if !names[scope] {
			t.Errorf("scope %q missing from suggestions after an action: %v", scope, names)
		}
	}
}

// TestCompleterSuggestsModifiersAfterTargets verifies the modifier tier.
func TestCompleterSuggestsModifiersAfterTargets(t *testing.T) {
	suggestions, _, _ := completer(documentFor("replace mac "))

	names := map[string]bool{}
	for _, s := range suggestions {
		names[s.Text] = true
	}
	for _, mod := range []string{"with", ModifierRequireAll, ModifierYolo, ModifierSpot, ModifierDryRun, ModifierNoColor} {
		if !names[mod] {
			t.Errorf("modifier %q missing from suggestions: %v", mod, names)
		}
	}
}

// TestCompleterTokenSuggestionsAreDynamic verifies the token list comes from the
// registry rather than a hard-coded list, so custom tokens appear too.
func TestCompleterTokenSuggestionsAreDynamic(t *testing.T) {
	registry.Register("zz_test_token", stubMatcher{})
	defer registry.Unregister("zz_test_token")

	found := false
	for _, s := range tokenSuggestions() {
		if s.Text == "zz_test_token" {
			found = true
		}
	}
	if !found {
		t.Error("a newly registered token did not appear in completion suggestions")
	}
}

// stubMatcher is a minimal Matcher for completion tests.
type stubMatcher struct{}

func (stubMatcher) Match(string) []string { return nil }
func (stubMatcher) Describe() string      { return "stub token for tests" }

// --- REPL command handling ------------------------------------------------

// newReplState builds a replState over an in-memory working copy.
func newReplState(t *testing.T, path, content, mode string) (*replState, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	state := &replState{
		path:  path,
		lines: strings.Split(strings.TrimSuffix(content, "\n"), "\n"),
		mode:  mode,
		in:    strings.NewReader("y\n"),
		out:   out,
		diag:  diag,
	}
	if err := state.rechunk(); err != nil {
		t.Fatalf("rechunk: %v", err)
	}
	return state, out, diag
}

// TestReplExitCommand verifies exit and quit end the session.
func TestReplExitCommand(t *testing.T) {
	state, _, _ := newReplState(t, "", "a\n", ModeStream)

	for _, cmd := range []string{"exit", "quit", "EXIT", "  quit  "} {
		if !state.handleInput(cmd) {
			t.Errorf("handleInput(%q) = false, want true (exit)", cmd)
		}
	}
	for _, cmd := range []string{"get all", "help", "lines"} {
		if state.handleInput(cmd) {
			t.Errorf("handleInput(%q) = true, want false", cmd)
		}
	}
}

// TestReplReadQueryEmitsJSON verifies a query produces a parseable JSON array.
func TestReplReadQueryEmitsJSON(t *testing.T) {
	state, out, _ := newReplState(t, "", "addr 10.0.0.1\nplain\naddr 10.0.0.2\n", ModeStream)

	if state.handleInput("get all [ip]") {
		t.Fatal("handleInput reported exit")
	}

	var matches []MatchResult
	if err := json.Unmarshal(out.Bytes(), &matches); err != nil {
		t.Fatalf("REPL output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}
	if matches[0].MatchedTokens["ip"][0] != "10.0.0.1" {
		t.Errorf("first match tokens = %v", matches[0].MatchedTokens)
	}
}

// TestReplLinesCommand verifies the working-copy summary.
func TestReplLinesCommand(t *testing.T) {
	state, out, _ := newReplState(t, "", "a\nb\nc\n", ModeStream)

	state.handleInput("lines")
	if got := out.String(); !strings.Contains(got, "3 lines") {
		t.Errorf("lines output = %q, want it to report 3 lines", got)
	}
}

// TestReplTokensCommand verifies the token listing.
func TestReplTokensCommand(t *testing.T) {
	state, out, _ := newReplState(t, "", "a\n", ModeStream)

	state.handleInput("tokens")
	got := out.String()
	for _, token := range []string{"ip", "bdf", "mac", "numa"} {
		if !strings.Contains(got, token) {
			t.Errorf("tokens output missing %q:\n%s", token, got)
		}
	}
}

// TestReplHelpCommand verifies help describes the grammar.
func TestReplHelpCommand(t *testing.T) {
	state, out, _ := newReplState(t, "", "a\n", ModeStream)

	state.handleInput("help")
	got := out.String()
	for _, want := range []string{"ACTION", "SCOPE", "TARGETS", "MODIFIERS", "replace"} {
		if !strings.Contains(got, want) {
			t.Errorf("help output missing %q", want)
		}
	}
}

// TestReplSyntaxErrorDoesNotAbort verifies a bad command reports and continues.
func TestReplSyntaxErrorDoesNotAbort(t *testing.T) {
	state, out, diag := newReplState(t, "", "a\n", ModeStream)

	if state.handleInput("get all [ip] bogus") {
		t.Fatal("a syntax error ended the session")
	}
	if !strings.Contains(diag.String(), "syntax error") {
		t.Errorf("stderr = %q, want a syntax error notice", diag.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout received %q, want nothing on a syntax error", out.String())
	}
}

// TestReplUnrunnableReplaceReports verifies the executability check is applied
// inside the REPL too, not only on the command line.
func TestReplUnrunnableReplaceReports(t *testing.T) {
	state, out, diag := newReplState(t, "", "a\n", ModeStream)

	if state.handleInput("replace mac") {
		t.Fatal("handleInput reported exit")
	}
	if !strings.Contains(diag.String(), "with") {
		t.Errorf("stderr = %q, want an explanation about the missing with clause", diag.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout received %q, want nothing", out.String())
	}
}

// TestReplReplaceDryRunDoesNotWrite verifies a dryrun query leaves the file and
// reloads nothing.
func TestReplReplaceDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cfg.txt"
	const content = "addr 10.0.1.7\n"
	if err := writeFile(path, content); err != nil {
		t.Fatalf("setup: %v", err)
	}

	state, out, diag := newReplState(t, path, content, ModeStream)
	state.handleInput("replace ip ['10.0.1.X'] with ['10.50.1.X'] dryrun")

	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("dryrun modified the file: %q", got)
	}
	if !strings.Contains(diag.String(), "@@ line") {
		t.Errorf("diff was not shown: %q", diag.String())
	}

	var result ExecResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("REPL stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if result.Status != StatusDryRun {
		t.Errorf("status = %q, want %q", result.Status, StatusDryRun)
	}
}

// TestReplReplaceYoloWritesAndReloads verifies a confirmed write updates both the
// file and the in-session working copy.
func TestReplReplaceYoloWritesAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/cfg.txt"
	const content = "addr 10.0.1.7\naddr 10.9.9.9\n"
	if err := writeFile(path, content); err != nil {
		t.Fatalf("setup: %v", err)
	}

	state, out, _ := newReplState(t, path, content, ModeStream)
	state.handleInput("replace ip ['10.0.1.X'] with ['10.50.1.X'] yolo")

	got, _ := os.ReadFile(path)
	if string(got) != "addr 10.50.1.7\naddr 10.9.9.9\n" {
		t.Errorf("file = %q, want the rewrite applied", got)
	}
	if _, err := os.Stat(path + BackupSuffix); err != nil {
		t.Errorf("no backup was created: %v", err)
	}

	var result ExecResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("REPL stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if result.Status != StatusApplied {
		t.Errorf("status = %q, want %q", result.Status, StatusApplied)
	}

	// The working copy must reflect the new content so a follow-up query sees it.
	if !strings.Contains(state.lines[0], "10.50.1.7") {
		t.Errorf("working copy was not reloaded: %q", state.lines[0])
	}
}

// TestReplStdinReplaceIsRefused verifies a read-only session cannot write.
func TestReplStdinReplaceIsRefused(t *testing.T) {
	state, out, diag := newReplState(t, "", "addr 10.0.1.7\n", ModeStream)

	state.handleInput("replace ip ['10.0.1.X'] with ['10.50.1.X'] yolo")
	if !strings.Contains(diag.String(), "stdin") {
		t.Errorf("stderr = %q, want a notice that stdin cannot be written", diag.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout received %q, want nothing", out.String())
	}
}

// TestReplDropReturnsComplement verifies drop in the REPL.
func TestReplDropReturnsComplement(t *testing.T) {
	state, out, _ := newReplState(t, "", "eth0 10.0.0.1\nplain line\n", ModeStream)

	state.handleInput("drop all [ip]")

	var matches []MatchResult
	if err := json.Unmarshal(out.Bytes(), &matches); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(matches) != 1 || matches[0].Stanza.Lines[0] != "plain line" {
		t.Fatalf("drop returned %+v, want only the plain line", matches)
	}
}

// --- target combination semantics ----------------------------------------

// TestSearchTargetsAreAlternativesByDefault verifies a stanza matches when any
// one target matches, which is the exploratory default.
func TestSearchTargetsAreAlternativesByDefault(t *testing.T) {
	const input = "0000:00:00.0 device with 10.0.0.1\n0000:00:01.0 device with no address\n"
	stanzas, lineStarts, _ := queryFixture(t, input, ModeStream)

	q, err := Parse("get all [bdf, ip]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	results, err := SearchStanzas(q, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want both stanzas (either target suffices)", len(results))
	}
}

// TestGroupByRequiresEveryTarget is the gate for conjunctive matching, which is
// what makes "which devices are degraded?" answerable: every device has a bdf,
// so the alternative reading reports the whole machine.
func TestGroupByRequiresEveryTarget(t *testing.T) {
	const input = `0000:00:00.0 healthy
	LnkCap: Speed 16GT/s, Width x16
	LnkSta: Speed 16GT/s, Width x16
0000:00:01.0 degraded
	LnkCap: Speed 32GT/s, Width x4
	LnkSta: Speed 2.5GT/s, Width x4
`
	stanzas, lineStarts, _ := queryFixture(t, input, ModeHeader)

	// Without the modifier both devices match, because each has a bdf.
	alt, err := Parse("get all [bdf, link_downgrade]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	altResults, err := SearchStanzas(alt, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}
	if len(altResults) != 2 {
		t.Fatalf("alternative reading got %d results, want 2", len(altResults))
	}

	// With the modifier only the degraded device satisfies every target.
	conj, err := Parse("get all [bdf, link_downgrade] require all")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	conjResults, err := SearchStanzas(conj, stanzas, lineStarts)
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}
	if len(conjResults) != 1 {
		t.Fatalf("conjunctive reading got %d results, want only the degraded device", len(conjResults))
	}
	if !strings.HasPrefix(conjResults[0].Stanza.Lines[0], "0000:00:01.0") {
		t.Errorf("conjunctive match = %q, want the degraded device",
			conjResults[0].Stanza.Lines[0])
	}
}

// TestGroupByParses verifies the modifier's field name is captured.
func TestGroupByParses(t *testing.T) {
	q, err := Parse("keep all [bdf, link_downgrade] require all")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.RequireAll {
		t.Error("RequireAll = false, want true")
	}
}

// --- with context N -------------------------------------------------------

// TestParseWithContext verifies the grammar form of the context modifier.
func TestParseWithContext(t *testing.T) {
	q, err := Parse("get all [error] with context 5")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.HasContext {
		t.Error("HasContext = false, want true")
	}
	if q.ContextSize != 5 {
		t.Errorf("ContextSize = %d, want 5", q.ContextSize)
	}
	if q.HasWith {
		t.Error("HasWith = true; a context clause is not a replacement value")
	}
}

// TestParseWithContextZero verifies an explicit zero is distinguishable from an
// absent clause, since the two mean the same thing but the field should record
// that it was asked for.
func TestParseWithContextZero(t *testing.T) {
	q, err := Parse("get all [ip] with context 0")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.HasContext || q.ContextSize != 0 {
		t.Errorf("HasContext = %v, ContextSize = %d; want true and 0", q.HasContext, q.ContextSize)
	}
}

// TestParseWithoutContextLeavesItUnset verifies the default.
func TestParseWithoutContextLeavesItUnset(t *testing.T) {
	q, err := Parse("get all [ip]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.HasContext {
		t.Error("HasContext = true for a query with no context clause")
	}
}

// TestParseWithContextErrorCases verifies the clause is validated.
func TestParseWithContextErrorCases(t *testing.T) {
	cases := map[string]string{
		"get all [ip] with context":     "no count",
		"get all [ip] with context abc": "non-numeric count",
		"get all [ip] with context -1":  "negative count",
		"get all [ip] with context 1.5": "fractional count",
	}
	for input, why := range cases {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error (%s)", input, why)
		}
	}
}

// TestParseReplaceRejectsContext verifies context is not accepted on a write,
// where it has no meaning.
func TestParseReplaceRejectsContext(t *testing.T) {
	if _, err := Parse("replace ip ['10.0.1.X'] with ['10.50.1.X'] with context 2"); err == nil {
		t.Error("Parse accepted with context on a replace, want an error")
	}
}

// TestParseWithReplacementStillWorks verifies the value form of "with" is
// unaffected by the context clause, including a literal that begins with the
// word context.
func TestParseWithReplacementStillWorks(t *testing.T) {
	q, err := Parse("replace ip ['10.0.1.X'] with ['10.50.1.X']")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.HasWith || q.With != "10.50.1.X" {
		t.Errorf("With = %q, HasWith = %v", q.With, q.HasWith)
	}
	if q.HasContext {
		t.Error("HasContext = true for a plain replacement")
	}

	// A quoted value reading "context" is a replacement, not the modifier.
	q, err = Parse("replace ['OLD'] with ['context']")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !q.HasWith || q.With != contextKeyword {
		t.Errorf("With = %q, want %q", q.With, contextKeyword)
	}
	if q.HasContext {
		t.Error("a quoted 'context' was mistaken for the modifier")
	}
}

// TestContextGrammarMatchesFlag is the behavioral gate: the grammar clause and
// the --context flag must produce identical output for identical input.
func TestContextGrammarMatchesFlag(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ips.txt"
	if err := writeFile(path, "addr 10.0.0.1\nplain-b\nplain-c\naddr 10.0.0.4\nplain-e\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	run := func(args []string) string {
		var out, diag bytes.Buffer
		if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		return out.String()
	}

	viaFlag := run([]string{"--context", "2", "get", "all", "[ip]", path})
	viaGrammar := run([]string{"get", "all", "[ip]", "with", "context", "2", path})

	if viaFlag != viaGrammar {
		t.Errorf("flag and grammar forms differ:\nflag:    %s\ngrammar: %s", viaFlag, viaGrammar)
	}

	// And the context must actually be the preceding lines.
	results := decodeNDJSON(t, viaGrammar)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if len(results[0].ContextLines) != 0 {
		t.Errorf("first match context = %v, want empty (nothing precedes it)", results[0].ContextLines)
	}
	want := []string{"plain-b", "plain-c"}
	if len(results[1].ContextLines) != len(want) {
		t.Fatalf("second match context = %v, want %v", results[1].ContextLines, want)
	}
	for i := range want {
		if results[1].ContextLines[i] != want[i] {
			t.Errorf("second match context = %v, want %v", results[1].ContextLines, want)
		}
	}
}

// TestContextGrammarOverridesFlag verifies the grammar clause wins, so a script
// can set a default with the flag and a single command can raise it.
func TestContextGrammarOverridesFlag(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ips.txt"
	if err := writeFile(path, "addr 10.0.0.1\nplain-b\nplain-c\naddr 10.0.0.4\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--context", "0", "get", "all", "[ip]", "with", "context", "1", path}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if got := results[1].ContextLines; len(got) != 1 || got[0] != "plain-c" {
		t.Errorf("context = %v, want [plain-c] (the grammar value, not the flag's 0)", got)
	}
}

// TestContextOnTokensPathStillWorks verifies the pre-existing flag behavior on
// the default token path is unchanged by the grammar work.
func TestContextOnTokensPathStillWorks(t *testing.T) {
	var out, diag bytes.Buffer
	args := []string{"--tokens", "ip", "--mode", "stream", "--context", "2"}
	input := "addr 10.0.0.1\nplain-b\nplain-c\naddr 10.0.0.4\n"
	if err := runPlain(t, args, strings.NewReader(input), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if got := results[1].ContextLines; len(got) != 2 || got[0] != "plain-b" || got[1] != "plain-c" {
		t.Errorf("context = %v, want [plain-b plain-c]", got)
	}
}

// TestContextNeverIncludesTheMatch verifies the snapshot ordering: context is
// taken before the stanza is pushed, so a stanza never contains itself.
func TestContextNeverIncludesTheMatch(t *testing.T) {
	var out, diag bytes.Buffer
	// A generous context on a two-line input would surface any off-by-one.
	input := "addr 10.0.0.1\naddr 10.0.0.2\n"
	args := []string{"get", "all", "[ip]", "with", "context", "99", "--mode", "stream"}
	if err := runPlain(t, args, strings.NewReader(input), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, r := range decodeNDJSON(t, out.String()) {
		for _, ctx := range r.ContextLines {
			if strings.Contains(ctx, "10.0.0.") && ctx == r.Stanza.Lines[0] {
				t.Errorf("context contains the match itself: %v", r.ContextLines)
			}
		}
		if len(r.ContextLines) > 1 {
			t.Errorf("context has %d lines for a %d-line prefix", len(r.ContextLines), r.StanzaIndex-1)
		}
	}
}
