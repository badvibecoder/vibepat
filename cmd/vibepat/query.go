package main

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/badvibecoder/vibepat/internal/registry"
)

// nowFunc is the clock used to resolve the "today" scope. It is a variable so
// tests can pin the date instead of depending on when they run.
var nowFunc = time.Now

// WildcardOctet is the placeholder character for a single-octet wildcard in an
// IP replacement pattern, as in "10.0.1.X". It must be the fourth octet: the
// first three are matched literally.
const WildcardOctet = 'X'

// SearchResult couples a matched stanza with what was found in it.
type SearchResult struct {
	// Stanza is the matched stanza.
	Stanza *Stanza
	// StanzaIndex is its 1-based position in the input.
	StanzaIndex int
	// StartLine is the 1-based input line the stanza began on.
	StartLine int
	// Matched maps each target token to the values found in this stanza.
	Matched map[string][]string
}

// --- replacement patterns -------------------------------------------------

// ipMatcher matches IP-shaped text for wildcard and CIDR patterns.
var ipMatcher = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)

// selector is a compiled replacement or matching pattern for one target token.
//
// Literal targets match exact text. IP targets additionally understand the
// fourth-octet wildcard ("10.0.1.X") and CIDR prefixes ("10.0.1.0/24"), both of
// which preserve the host portion while rewriting the network portion.
type selector struct {
	token string
	// literal is set for an exact-string target.
	literal string
	// pattern is the compiled matcher, for non-literal targets.
	pattern *regexp.Regexp
	// apply rewrites a matched value; it is nil when the pattern only matches.
	apply func(match string) (string, bool)
}

// newSelector compiles the target value for token into a selector.
//
// A value that is not a wildcard or CIDR pattern is treated as an exact literal,
// because the grammar's ['literal'] form is meant to match text verbatim.
func newSelector(token, value string) (*selector, error) {
	sel := &selector{token: token}

	if token != registry.TokenIP {
		sel.literal = value
		sel.pattern = regexp.MustCompile(regexp.QuoteMeta(value))
		if value != "" {
			sel.apply = func(match string) (string, bool) { return "", false }
		}
		return sel, nil
	}

	// IP: decide between CIDR and fourth-octet wildcard.
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR prefix %q: %w", value, err)
		}
		sel.pattern = ipMatcher
		sel.apply = cidrRewriter(prefix)
		return sel, nil
	}

	if strings.ContainsRune(value, WildcardOctet) {
		// Only the fourth octet may be wildcarded, so the pattern must be
		// exactly four octets with X in the last position.
		parts := strings.Split(value, ".")
		if len(parts) != 4 || parts[3] != string(WildcardOctet) {
			return nil, fmt.Errorf(
				"invalid IP wildcard %q: %c is only allowed as the fourth octet, e.g. 10.0.1.%c",
				value, WildcardOctet, WildcardOctet)
		}
		for _, p := range parts[:3] {
			if _, err := strconv.Atoi(p); err != nil {
				return nil, fmt.Errorf("invalid IP wildcard %q: %q is not a numeric octet", value, p)
			}
		}
		quoted := regexp.QuoteMeta(strings.Join(parts[:3], "."))
		sel.pattern = regexp.MustCompile(quoted + `\.(\d{1,3})`)
		sel.apply = func(match string) (string, bool) {
			sub := regexp.MustCompile(quoted + `\.(\d{1,3})`).FindStringSubmatch(match)
			if len(sub) < 2 {
				return "", false
			}
			return sub[1], true
		}
		return sel, nil
	}

	// A fully specified address.
	sel.literal = value
	sel.pattern = regexp.MustCompile(regexp.QuoteMeta(value))
	if value != "" {
		sel.apply = func(match string) (string, bool) { return "", false }
	}
	return sel, nil
}

// cidrRewriter returns a function that captures the host portion of an address
// inside prefix, so it can be re-appended to a new network.
func cidrRewriter(prefix netip.Prefix) func(string) (string, bool) {
	return func(match string) (string, bool) {
		addr, err := netip.ParseAddr(match)
		if err != nil {
			return "", false
		}
		addr = addr.Unmap()
		if !prefix.Contains(addr) {
			return "", false
		}
		bits := prefix.Bits()
		if bits < 0 || bits > 32 || !addr.Is4() {
			return "", false
		}
		// The host portion is everything below the prefix length.
		v := addr.As4()
		hostBits := 32 - bits
		host := uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
		host &= (1 << hostBits) - 1
		return strconv.FormatUint(uint64(host), 10), true
	}
}

// wantsCapture reports whether the selector carries a value to substitute.
func (s *selector) wantsCapture() bool { return s.apply != nil }

// matchStanza returns the distinct values this selector finds in a stanza.
//
// A selector with no pattern is a plain token name, so the semantic matcher from
// the registry decides what counts. That is what makes `get all [ip]` find IP
// addresses rather than the literal text "ip". A selector with a pattern uses
// that pattern instead.
func (s *selector) matchStanza(text string) []string {
	if s.pattern == nil {
		if s.literal == "" {
			return nil
		}
		// A registered token name with no pattern: defer to the registry.
		if matcher, ok := registry.Lookup(s.literal); ok {
			return matcher.Match(text)
		}
		if strings.Contains(text, s.literal) {
			return []string{s.literal}
		}
		return nil
	}

	if s.literal != "" {
		if strings.Contains(text, s.literal) {
			return []string{s.literal}
		}
		return nil
	}
	return dedupeStrings(s.pattern.FindAllString(text, -1))
}

// rewriteLine replaces every occurrence in line according to the selector and
// the replacement text.
//
// Three shapes are handled:
//
//   - a semantic token name with no pattern ("replace mac with ['REDACTED']"):
//     every value the registry finds is replaced
//   - a wildcard or CIDR pattern ("replace ip ['10.0.1.X'] ..."): the captured
//     value is substituted into the replacement, so 10.0.1.7 becomes 10.50.1.7
//   - an exact literal string, which is replaced verbatim
func (s *selector) rewriteLine(line, replacement string) (string, bool) {
	// A pattern-less selector whose literal is a registered token name means
	// "every value of this token", so the registry decides what to replace.
	// Without this, `replace mac ...` would rewrite the literal text "mac".
	if s.pattern == nil {
		if matcher, ok := registry.Lookup(s.literal); ok {
			changed := false
			out := line
			for _, value := range matcher.Match(line) {
				if !strings.Contains(out, value) {
					continue
				}
				out = strings.ReplaceAll(out, value, replacement)
				changed = true
			}
			return out, changed
		}
	}

	if s.literal != "" {
		if !strings.Contains(line, s.literal) {
			return line, false
		}
		if replacement == "" {
			return strings.ReplaceAll(line, s.literal, ""), true
		}
		return strings.ReplaceAll(line, s.literal, replacement), true
	}

	changed := false
	out := s.pattern.ReplaceAllStringFunc(line, func(match string) string {
		captured, ok := s.apply(match)
		if !ok {
			return match
		}
		changed = true
		// Re-insert the captured value wherever the wildcard appears in the
		// replacement, so the host portion survives the rewrite.
		return strings.ReplaceAll(replacement, string(WildcardOctet), captured)
	})
	return out, changed
}

// --- query planning -------------------------------------------------------

// buildSelectors compiles the query's targets into selectors.
//
// When the query has a "with" clause, the targets describe what to rewrite, so
// the target value is the pattern to find. Otherwise the targets are semantic
// token names and the registry supplies the matching.
func buildSelectors(q *Query) ([]*selector, error) {
	if len(q.Targets) == 0 {
		return nil, nil
	}

	selectors := make([]*selector, 0, len(q.Targets))
	for _, target := range q.Targets {
		// A target paired with a bracketed pattern is a semantic token whose
		// values must match that pattern: "replace ip ['10.0.1.X']".
		if value, paired := q.Patterns[target]; paired && registry.IsRegistered(target) {
			sel, err := newSelector(strings.ToLower(target), value)
			if err != nil {
				return nil, err
			}
			selectors = append(selectors, sel)
			continue
		}

		// A registered token name with no pattern matches any value of that
		// token, which the registry knows how to find. Representing it as a
		// pattern-less selector keeps that distinction visible.
		if registry.IsRegistered(target) {
			selectors = append(selectors, &selector{token: strings.ToLower(target), literal: strings.ToLower(target)})
			continue
		}

		// Anything else is a literal string to find.
		sel, err := newSelector("literal", target)
		if err != nil {
			return nil, err
		}
		selectors = append(selectors, sel)
	}
	return selectors, nil
}

// targetTokenName infers which token a target refers to. An explicit registered
// token name wins; otherwise an IP-shaped value is treated as ip, and anything
// else is a literal.
func targetTokenName(target string) string {
	if registry.IsRegistered(target) {
		return strings.ToLower(target)
	}
	if strings.ContainsAny(target, "./") && ipMatcher.MatchString(target) {
		return registry.TokenIP
	}
	return "literal"
}

// targetValue returns the value to match for a target. A registered token name
// carries no value of its own.
func targetValue(target string) string {
	if registry.IsRegistered(target) {
		return ""
	}
	return target
}

// SearchStanzas selects the stanzas a query matches, honoring the scope.
//
// Scope semantics: first N and last N bound the number of *matches reported*,
// not the size of the input window, so `get first 3 [ip]` returns three matching
// stanzas even if it must read further into the input to find them.
func SearchStanzas(q *Query, stanzas []*Stanza, lineStarts []int) ([]SearchResult, error) {
	selectors, err := buildSelectors(q)
	if err != nil {
		return nil, err
	}

	var matches []SearchResult
	for i, stanza := range stanzas {
		matched := map[string][]string{}

		if len(selectors) == 0 {
			// No target: every stanza matches, which is "get all".
			matched = nil
		} else {
			for _, sel := range selectors {
				values := sel.matchStanza(stanza.RawText)
				if len(values) > 0 {
					matched[sel.token] = append(matched[sel.token], values...)
				}
			}
			if !matchSatisfied(q, selectors, matched) {
				continue
			}
		}

		start := 0
		if i < len(lineStarts) {
			start = lineStarts[i]
		}
		matches = append(matches, SearchResult{
			Stanza:      stanza,
			StanzaIndex: i + 1,
			StartLine:   start,
			Matched:     matched,
		})
	}

	return applyScope(q, matches), nil
}

// matchSatisfied reports whether a stanza's matches satisfy the query.
//
// By default the targets are alternatives: a stanza matches if any one of them
// is present, so `get all [bdf, numa]` reports devices that have either. That is
// the useful default for exploration.
//
// "require all" switches to conjunctive matching. It exists because the
// alternative reading is wrong for questions of the form "which devices are
// degraded?": every device has a bdf, so `get all [bdf, link_downgrade]` would
// report the whole machine, whereas `get all [bdf, link_downgrade] require all`
// reports only the degraded ones.
func matchSatisfied(q *Query, selectors []*selector, matched map[string][]string) bool {
	if len(matched) == 0 {
		return false
	}
	if !q.RequireAll {
		return true
	}
	for _, sel := range selectors {
		if len(matched[sel.token]) == 0 {
			return false
		}
	}
	return true
}

// applyScope trims the match list according to the query's scope.
func applyScope(q *Query, matches []SearchResult) []SearchResult {
	switch q.Scope {
	case ScopeFirstN:
		if q.ScopeN > 0 && q.ScopeN < len(matches) {
			return matches[:q.ScopeN]
		}
	case ScopeLastN:
		if q.ScopeN > 0 && q.ScopeN < len(matches) {
			return matches[len(matches)-q.ScopeN:]
		}
	case ScopeStanza:
		if q.ScopeN >= 1 && q.ScopeN <= len(matches) {
			return matches[q.ScopeN-1 : q.ScopeN]
		}
		return nil
	case ScopeToday:
		today := todayPatterns()
		kept := matches[:0:0]
		for _, m := range matches {
			if containsAnyDate(m.Stanza.RawText, today) {
				kept = append(kept, m)
			}
		}
		return kept
	}
	return matches
}

// BuildQueryMutations produces the line mutations for a replace query.
//
// Each matching stanza's lines are rewritten in place; the returned mutations
// reference original input line numbers so the existing execution engine can
// validate and commit them.
func BuildQueryMutations(q *Query, results []SearchResult, originalLines []string) ([]Mutation, error) {
	if !q.IsWrite() {
		return nil, nil
	}

	selectors, err := buildSelectors(q)
	if err != nil {
		return nil, err
	}

	var mutations []Mutation
	for _, res := range results {
		// Locate the stanza's first line within the original input so every
		// mutation carries an absolute line number.
		base := res.StartLine - 1
		for i, line := range res.Stanza.Lines {
			lineNo := base + i + 1
			if lineNo < 1 || lineNo > len(originalLines) {
				continue
			}
			if originalLines[lineNo-1] != line {
				// The stanza text did not line up with the input; skip rather
				// than mutate the wrong line.
				continue
			}

			updated := line
			for _, sel := range selectors {
				if next, changed := sel.rewriteLine(updated, q.With); changed {
					updated = next
				}
			}
			if updated == line {
				continue
			}
			mutations = append(mutations, Mutation{
				LineNumber:   lineNo,
				OriginalText: line,
				ModifiedText: updated,
			})
		}
	}

	return dedupeMutations(mutations), nil
}

// dedupeMutations keeps the first mutation per line, since a ChangePlan may
// change any line only once.
func dedupeMutations(in []Mutation) []Mutation {
	seen := make(map[int]bool, len(in))
	out := make([]Mutation, 0, len(in))
	for _, m := range in {
		if seen[m.LineNumber] {
			continue
		}
		seen[m.LineNumber] = true
		out = append(out, m)
	}
	return out
}

// --- helpers --------------------------------------------------------------

// dedupeStrings removes repeats while preserving first-seen order.
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// todayPatterns returns the text forms that represent today's date in the log
// formats vibepat targets. This is a text heuristic, not a date parser: it
// answers "does this line look like it is from today".
func todayPatterns() []string {
	now := nowFunc()
	return []string{
		now.Format("2006-01-02"), // ISO / RFC3339
		now.Format("Jan 2"),      // syslog, space-padded
		now.Format("Jan 02"),     // syslog, zero-padded
		now.Format("Jan _2"),     // syslog single-digit day
		now.Format("01/02/2006"), // US
		now.Format("02/01/2006"), // EU
		now.Format("2006/01/02"), // slash ISO
		now.Format("Mon Jan 2"),  // RFC1123-ish
	}
}

// containsAnyDate reports whether text contains any of the date forms.
func containsAnyDate(text string, patterns []string) bool {
	for _, p := range patterns {
		if p != "" && strings.Contains(text, p) {
			return true
		}
	}
	return false
}
