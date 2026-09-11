package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Grammar actions.
const (
	// ActionGet reports matching stanzas without modifying anything. It is the
	// default when no action word is given.
	ActionGet = "get"
	// ActionKeep reports matching stanzas; a read-only filter.
	ActionKeep = "keep"
	// ActionDrop reports stanzas that do NOT match; a read-only filter.
	ActionDrop = "drop"
	// ActionReplace rewrites matching text and can write to disk.
	ActionReplace = "replace"
)

// Grammar scopes.
const (
	// ScopeAll examines every unit.
	ScopeAll = "all"
	// ScopeFirstN examines the first N units.
	ScopeFirstN = "first"
	// ScopeLastN examines the last N units.
	ScopeLastN = "last"
	// ScopeToday examines units whose text contains today's date.
	ScopeToday = "today"
	// ScopeStanza examines a single stanza by 1-based index.
	ScopeStanza = "stanza"
)

// contextKeyword names the "with context N" clause, which embeds the N lines
// preceding each match. It mirrors the --context flag so the same behavior is
// reachable from the grammar.
const contextKeyword = "context"

// Grammar modifier keywords.
const (
	// ModifierYolo skips the confirmation prompt on a write.
	ModifierYolo = "yolo"
	// ModifierForce is an alias for ModifierYolo.
	ModifierForce = "force"
	// ModifierSpot shows a random sample of the pending diffs.
	ModifierSpot = "spot"
	// ModifierDryRun computes output without writing.
	ModifierDryRun = "dryrun"
	// ModifierRequireAll requires every target to match rather than any one.
	ModifierRequireAll = "require"
	// ModifierNoColor disables ANSI color.
	ModifierNoColor = "no-color"
)

// ActionNames lists every valid action, for TAB completion and error messages.
var ActionNames = []string{ActionGet, ActionKeep, ActionDrop, ActionReplace}

// ScopeNames lists every valid scope.
var ScopeNames = []string{ScopeAll, ScopeFirstN, ScopeLastN, ScopeToday, ScopeStanza}

// Query is the parsed form of an ACTION SCOPE TARGET MODIFIER command.
type Query struct {
	// Action is one of the Action* constants.
	Action string
	// Scope is one of the Scope* constants, or "" when unspecified (which means
	// ScopeAll).
	Scope string
	// ScopeN is the count for ScopeFirstN and ScopeLastN, or the 1-based index
	// for ScopeStanza.
	ScopeN int
	// Targets are the semantic token names to match, e.g. ["ip", "bdf"].
	Targets []string
	// With holds the replacement text from a "with ['X']" clause.
	With string
	// ContextSize is the number of preceding lines to embed, set by a
	// "with context N" clause. Zero means none, matching the --context default.
	ContextSize int
	// HasContext records whether a "with context" clause was present, so an
	// explicit 0 is distinguishable from its absence.
	HasContext bool
	// Patterns maps a target token name to the value it should match, for the
	// "replace ip ['10.0.1.X']" form where the bracketed value belongs to the
	// token named just before it.
	Patterns map[string]string
	// HasWith records whether a "with" clause was present at all, so an empty
	// replacement ("delete this text") is distinguishable from its absence.
	HasWith bool
	// Modifiers are the execution modifiers: yolo, force, spot, dryrun, no-color.
	Modifiers []string
	// SpotN is the sample size from a "spot N" modifier; 0 when unset.
	SpotN int
	// RequireAll is set by the "require all" modifier, which switches target
	// matching from "any target matches" to "every target must match".
	RequireAll bool
	// Raw is the original command text, preserved for diagnostics and echo.
	Raw string
}

// ParseError describes a grammar error, including the position of the offending
// token so an interactive user can see exactly where parsing stopped.
type ParseError struct {
	// Token is the text that could not be parsed.
	Token string
	// Position is the 1-based index of Token within the token stream.
	Position int
	// Expected describes what the parser wanted instead.
	Expected string
	// Message is the complete human-readable error.
	Message string
}

func (e *ParseError) Error() string { return e.Message }

// newParseError builds a ParseError with a consistent message shape.
func newParseError(token string, pos int, expected string, got string) *ParseError {
	return &ParseError{
		Token:    token,
		Position: pos,
		Expected: expected,
		Message:  fmt.Sprintf("unexpected %s at position %d: %s", got, pos, expected),
	}
}

// Parse translates a raw command string into a Query.
//
// The grammar is [ACTION] [SCOPE] [TARGETS] [MODIFIERS], all tokens optional
// except that a replace requires a "with" clause and a tokenized target. The
// action defaults to get, and the scope defaults to all.
//
// Examples:
//
//	get all [bdf, numa]
//	replace ip ['10.0.1.X'] with ['10.50.1.X'] yolo
//	replace mac yolo
//	keep first 3 [error]
//	get all [bdf, link_downgrade] require all
func Parse(input string) (*Query, error) {
	tokens, err := tokenize(input)
	if err != nil {
		return nil, err
	}

	// The scope defaults to all, so a command may omit it entirely.
	q := &Query{Action: ActionGet, Scope: ScopeAll, Raw: strings.TrimSpace(input)}
	if len(tokens) == 0 {
		return q, nil
	}

	pos := 0

	// ACTION is optional; an omitted action defaults to get.
	if isAction(tokens[0]) {
		q.Action = strings.ToLower(tokens[0])
		pos++
	}

	// SCOPE is optional; "all" is the default and may be stated explicitly.
	if pos < len(tokens) {
		switch strings.ToLower(tokens[pos]) {
		case ScopeAll:
			q.Scope = ScopeAll
			pos++
		case ScopeFirstN, ScopeLastN:
			q.Scope = strings.ToLower(tokens[pos])
			pos++
			n, next, err := parseScopeCount(tokens, pos)
			if err != nil {
				return nil, err
			}
			q.ScopeN = n
			pos = next
		case ScopeToday:
			q.Scope = ScopeToday
			pos++
		case ScopeStanza:
			q.Scope = ScopeStanza
			pos++
			n, next, err := parseScopeCount(tokens, pos)
			if err != nil {
				return nil, err
			}
			q.ScopeN = n
			pos = next
		}
	}

	// TARGETS may be a bracketed list, a quoted literal, or a bare token list.
	if pos < len(tokens) && !isModifierKeyword(tokens[pos]) {
		targets, patterns, next, err := parseTargets(tokens, pos, q.Action)
		if err != nil {
			return nil, err
		}
		q.Targets = targets
		q.Patterns = patterns
		pos = next
	}

	// MODIFIERS, including the "with" clause, which we treat as a modifier
	// because it is only meaningful on a replace.
	for pos < len(tokens) {
		tok := strings.ToLower(tokens[pos])

		switch tok {
		case "with":
			pos++
			if pos >= len(tokens) {
				return nil, newParseError("with", pos,
					"\"with\" must be followed by \"context N\" or a replacement value", "end of input")
			}

			// "with context N" embeds preceding lines rather than naming a
			// replacement, so it is handled before the value form.
			if strings.EqualFold(tokens[pos], contextKeyword) {
				pos++
				size, next, err := parseContextCount(tokens, pos)
				if err != nil {
					return nil, err
				}
				q.ContextSize = size
				q.HasContext = true
				pos = next
				continue
			}

			value, err := unquote(tokens[pos])
			if err != nil {
				return nil, newParseError(tokens[pos], pos+1, "a quoted replacement value", err.Error())
			}
			q.With = value
			q.HasWith = true
			pos++

		case ModifierRequireAll:
			pos++
			if pos >= len(tokens) || strings.ToLower(tokens[pos]) != "all" {
				return nil, newParseError(tokenAt(tokens, pos), pos+1,
					"\"require\" must be followed by \"all\"", "something else")
			}
			q.RequireAll = true
			q.Modifiers = appendUnique(q.Modifiers, ModifierRequireAll)
			pos++

		case ModifierSpot:
			pos++
			// "spot" may be followed by an optional count.
			if pos < len(tokens) {
				if n, err := strconv.Atoi(tokens[pos]); err == nil {
					if n < 0 {
						return nil, newParseError(tokens[pos], pos+1, "a non-negative spot count", "a negative number")
					}
					q.SpotN = n
					pos++
				}
			}
			q.Modifiers = appendUnique(q.Modifiers, ModifierSpot)

		case ModifierYolo, ModifierForce, ModifierDryRun, ModifierNoColor:
			q.Modifiers = appendUnique(q.Modifiers, tok)
			pos++

		default:
			return nil, newParseError(tokens[pos], pos+1,
				"a target list, a modifier, or \"with\"", fmt.Sprintf("%q", tokens[pos]))
		}
	}

	if err := q.Validate(); err != nil {
		return nil, err
	}
	return q, nil
}

// Validate checks the semantic constraints that the token grammar cannot
// express, so a query is either fully usable or rejected with a clear reason.
func (q *Query) Validate() error {
	switch q.Action {
	case ActionGet, ActionKeep, ActionDrop:
		if q.HasWith {
			return fmt.Errorf("%s does not take a \"with\" clause; only replace does", q.Action)
		}
	case ActionReplace:
		if q.HasContext {
			return fmt.Errorf("replace does not take a \"with context\" clause; context applies to read-only queries")
		}
		// A replace without a "with" clause is grammatically valid but not
		// executable. That is reported by ValidateExecutable so the parser can
		// still describe the command (and the REPL can offer completion for it).
	default:
		return fmt.Errorf("unknown action %q; want one of %s", q.Action, strings.Join(ActionNames, ", "))
	}

	// A targetless query is meaningful only when it means "everything", which is
	// the scope "all". "get all" is the most basic command in the grammar, so it
	// must not be rejected for lacking a token.
	if len(q.Targets) == 0 && q.Scope != ScopeAll {
		return fmt.Errorf("no target given; name at least one token, or use all")
	}
	return nil
}

// ValidateExecutable reports whether the query can actually be run. It is
// separate from Validate because a command may be well-formed grammar without
// being complete enough to execute, and the parser should accept the former.
func (q *Query) ValidateExecutable() error {
	if err := q.Validate(); err != nil {
		return err
	}
	if q.Action == ActionReplace && !q.HasWith {
		return fmt.Errorf("replace requires a \"with ['...']\" clause naming the replacement")
	}
	if q.IsWrite() && len(q.Patterns) == 0 && len(q.Targets) == 0 {
		return fmt.Errorf("replace requires a target naming what to rewrite")
	}
	return nil
}

// parseScopeCount reads the N in "first N" / "stanza N", or infers N=1 when the
// clause is written without a number.
func parseScopeCount(tokens []string, pos int) (int, int, error) {
	if pos >= len(tokens) {
		return 1, pos, nil
	}
	n, err := strconv.Atoi(tokens[pos])
	if err != nil {
		// Any non-numeric token means the count was omitted.
		return 1, pos, nil
	}
	if n < 1 {
		return 0, pos, newParseError(tokens[pos], pos+1, "a count of at least 1", "zero or negative")
	}
	return n, pos + 1, nil
}

// parseContextCount reads the N in "with context N". Unlike a scope count, the
// number is required: "with context" alone would be ambiguous about how much
// context was wanted.
func parseContextCount(tokens []string, pos int) (int, int, error) {
	if pos >= len(tokens) {
		return 0, pos, newParseError(contextKeyword, pos,
			"\"context\" must be followed by a line count", "end of input")
	}
	n, err := strconv.Atoi(tokens[pos])
	if err != nil {
		return 0, pos, newParseError(tokens[pos], pos+1,
			"a line count after \"context\"", fmt.Sprintf("%q", tokens[pos]))
	}
	if n < 0 {
		return 0, pos, newParseError(tokens[pos], pos+1,
			"a non-negative line count", "a negative number")
	}
	return n, pos + 1, nil
}

// parseTargets reads the target list starting at pos.
//
// Two shapes are accepted, and which one applies depends on the action:
//
//	get all [bdf, numa]      a bracketed list of token names
//	replace ip ['10.0.1.X']  a token name followed by the pattern it should match
//
// The second form is why the action is a parameter: in a replace the bracketed
// value is the pattern belonging to the token named just before it, whereas in a
// read query the bracket is the list itself.
func parseTargets(tokens []string, pos int, action string) ([]string, map[string]string, int, error) {
	// A leading bracketed value with no preceding token name is a bare pattern.
	if strings.HasPrefix(tokens[pos], "[") {
		value, err := unquote(tokens[pos])
		if err != nil {
			return nil, nil, pos, newParseError(tokens[pos], pos+1, "a valid target", err.Error())
		}
		if value == "" {
			return nil, nil, pos + 1, nil
		}
		if action == ActionReplace {
			return []string{value}, map[string]string{}, pos + 1, nil
		}
		return splitTargetList(value), nil, pos + 1, nil
	}

	var (
		targets  []string
		patterns = map[string]string{}
		lastBare string
	)

	for pos < len(tokens) {
		tok := tokens[pos]

		// A modifier or a new action ends the target run.
		if isModifierKeyword(tok) || isAction(tok) {
			break
		}
		// A scope word ends the run too.
		if isScopeWord(strings.ToLower(tok)) {
			break
		}
		// A bare number is a count, not a target.
		if _, err := strconv.Atoi(tok); err == nil {
			break
		}

		if strings.HasPrefix(tok, "[") {
			value, err := unquote(tok)
			if err != nil {
				return nil, nil, pos, newParseError(tok, pos+1, "a valid target", err.Error())
			}
			if action == ActionReplace && lastBare != "" {
				// The bracketed value is the pattern for the token named just
				// before it: "replace ip ['10.0.1.X']".
				patterns[lastBare] = value
			} else if value != "" {
				targets = append(targets, splitTargetList(value)...)
			}
			pos++
			continue
		}

		value, err := unquote(tok)
		if err != nil {
			return nil, nil, pos, newParseError(tok, pos+1, "a valid target", err.Error())
		}
		if value != "" {
			targets = append(targets, value)
			lastBare = value
		}
		pos++
	}

	return targets, patterns, pos, nil
}

// splitTargetList splits a bracketed "bdf, numa" body into individual names.
func splitTargetList(inner string) []string {
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// tokenize splits a command into tokens.
//
// Bracket groups are kept intact ("[bdf, numa]"), and single- or double-quoted
// strings are kept intact including any spaces, because both a target list and
// a replacement value are user text that must survive splitting. Quotes are
// preserved so unquote can distinguish a quoted literal from a bare token.
func tokenize(input string) ([]string, error) {
	var (
		tokens  []string
		current strings.Builder
		inBrack bool
		quote   byte
	)

	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	for i := 0; i < len(input); i++ {
		c := input[i]

		switch {
		case quote != 0:
			current.WriteByte(c)
			if c == quote {
				quote = 0
			}

		case c == '\'' || c == '"':
			quote = c
			current.WriteByte(c)

		case c == '[':
			inBrack = true
			current.WriteByte(c)

		case c == ']':
			inBrack = false
			current.WriteByte(c)

		case (c == ' ' || c == '\t' || c == '\n') && !inBrack:
			flush()

		default:
			current.WriteByte(c)
		}
	}

	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote in %q", quote, input)
	}
	if inBrack {
		return nil, fmt.Errorf("unterminated [ in %q", input)
	}
	flush()
	return tokens, nil
}

// unquote strips one layer of matching single or double quotes, and strips
// square brackets. Text without quotes is returned unchanged.
func unquote(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}

	if s[0] == '[' && strings.HasSuffix(s, "]") {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}

	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1], nil
		}
		if s[0] == '\'' || s[0] == '"' {
			return "", fmt.Errorf("unmatched quote in %q", s)
		}
	}
	return s, nil
}

// isAction reports whether tok names an action, case-insensitively.
func isAction(tok string) bool {
	switch strings.ToLower(tok) {
	case ActionGet, ActionKeep, ActionDrop, ActionReplace:
		return true
	}
	return false
}

// isModifierKeyword reports whether tok begins a modifier clause.
func isModifierKeyword(tok string) bool {
	switch strings.ToLower(tok) {
	case "with", ModifierRequireAll, ModifierSpot, ModifierYolo, ModifierForce, ModifierDryRun, ModifierNoColor:
		return true
	}
	return false
}

// tokenAt returns the token at pos, or a placeholder describing end of input.
func tokenAt(tokens []string, pos int) string {
	if pos < 0 || pos >= len(tokens) {
		return "<end>"
	}
	return tokens[pos]
}

// appendUnique appends v unless it is already present.
func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

// HasModifier reports whether the query carries the named modifier, treating
// "force" and "yolo" as the same modifier in either direction.
func (q *Query) HasModifier(name string) bool {
	want := canonicalModifier(name)
	for _, m := range q.Modifiers {
		if canonicalModifier(m) == want {
			return true
		}
	}
	return false
}

// canonicalModifier folds the force/yolo alias pair onto one canonical name.
func canonicalModifier(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == ModifierForce {
		return ModifierYolo
	}
	return name
}

// IsWrite reports whether the query can modify a file.
func (q *Query) IsWrite() bool { return q.Action == ActionReplace }
