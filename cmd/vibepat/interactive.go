package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	prompt "github.com/elk-language/go-prompt"
	istrings "github.com/elk-language/go-prompt/strings"

	"github.com/badvibecoder/vibepat/internal/registry"
)

// replPrefix is the prompt string shown in interactive mode.
const replPrefix = "vibepat> "

// replFileName is the pseudo-name reported for an interactive session, so
// diagnostics distinguish a REPL from a pipe.
const replFileName = "<interactive>"

// replEnv bundles the streams and terminal facts a REPL needs. Grouping them
// keeps the signatures readable as more flags accumulate.
type replEnv struct {
	// In supplies confirmation answers.
	In io.Reader
	// Out receives machine-readable command results.
	Out io.Writer
	// Diag receives the prompt, diffs, and notices.
	Diag io.Writer
	// Color enables ANSI styling of diffs.
	Color bool
	// StdinIsTTY reports whether stdin is an interactive terminal. The REPL
	// cannot run without one, because the line editor needs raw terminal mode.
	StdinIsTTY bool
}

// replState carries the context an interactive session accumulates between
// commands.
type replState struct {
	// path is the file under edit, or "" when the session reads stdin.
	path string
	// lines holds the session's working copy of the input.
	lines []string
	// stanzas is the chunked working copy, refreshed when lines change.
	stanzas []*Stanza
	// lineStarts[i] is the 1-based input line on which stanzas[i] began.
	lineStarts []int
	// mode is the chunking mode in force.
	mode string
	// color controls ANSI styling of diffs.
	color bool
	// out receives command results.
	out io.Writer
	// diag receives human-facing diffs and notices.
	diag io.Writer
	// in supplies confirmation answers.
	in io.Reader
}

// RunInteractive starts the Cisco-IOS-style REPL over the given input.
//
// If path is empty, the input came from a pipe and the session is read-only,
// because a stream cannot be written back.
//
// An interactive terminal is required: the line editor puts the terminal into
// raw mode, and attempting that on a pipe fails. Rather than panic, the caller
// is told to use pipe mode instead.
func RunInteractive(path string, lines []string, mode string, env replEnv) error {
	if !env.StdinIsTTY {
		return fmt.Errorf("interactive mode requires a terminal on stdin; " +
			"pass the grammar as arguments instead, e.g. vibepat get all [bdf]")
	}

	state := &replState{
		path:  path,
		lines: lines,
		mode:  mode,
		color: env.Color,
		in:    env.In,
		out:   env.Out,
		diag:  env.Diag,
	}
	if err := state.rechunk(); err != nil {
		return err
	}

	fmt.Fprintf(env.Diag, "vibepat interactive mode. Type 'help' for commands, 'exit' to quit.\n")
	if state.path == "" {
		fmt.Fprintf(env.Diag, "input is from stdin: queries are read-only.\n")
	} else {
		fmt.Fprintf(env.Diag, "editing %s\n", state.path)
	}

	p := prompt.New(
		func(input string) {
			// The go-prompt loop owns the terminal; the command logic lives in
			// handleInput so it can be tested without a PTY.
			state.handleInput(input)
		},
		prompt.WithCompleter(completer),
		prompt.WithPrefix(replPrefix),
		prompt.WithTitle("vibepat"),
		prompt.WithHistory([]string{}),
		// The exit checker runs before the executor, so "exit" ends the loop
		// without being parsed as a command.
		prompt.WithExitChecker(func(input string, breakline bool) bool {
			if !breakline {
				return false
			}
			switch strings.ToLower(strings.TrimSpace(input)) {
			case "exit", "quit":
				return true
			}
			return false
		}),
	)
	p.Run()
	return nil
}

// rechunk rebuilds the stanza list from the working lines.
func (s *replState) rechunk() error {
	sc, err := NewVisualScanner(strings.NewReader(strings.Join(s.lines, "\n")), s.mode, DefaultSampleSize)
	if err != nil {
		return err
	}
	var stanzas []*Stanza
	for {
		stanza, err := sc.NextStanza()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		stanzas = append(stanzas, stanza)
	}
	s.stanzas = stanzas
	s.lineStarts = lineIndex(s.lines, stanzas)
	return nil
}

// lineOf returns the 1-based input line on which stanza i began.
func (s *replState) lineOf(i int) int {
	if i < 0 || i >= len(s.lineStarts) {
		return 0
	}
	return s.lineStarts[i]
}

// handleInput runs one line of REPL input and reports whether the session should
// end. It performs no terminal I/O of its own beyond writing to the configured
// streams, so it is directly testable.
func (s *replState) handleInput(input string) (exit bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}

	switch strings.ToLower(input) {
	case "exit", "quit":
		return true
	case "tokens":
		s.printTokens()
		return false
	case "lines":
		fmt.Fprintf(s.out, "%d lines, %d stanzas\n", len(s.lines), len(s.stanzas))
		return false
	}

	// "help" and "help <topic>" surface the same reference the CLI prints, and
	// stay inside the session. An unknown topic is reported without leaving
	// interactive mode.
	if fields := strings.Fields(input); len(fields) > 0 &&
		(strings.EqualFold(fields[0], "help") || fields[0] == "?") {
		topic := "index"
		switch len(fields) {
		case 1:
			// Bare "help" in the REPL shows the index: the full manual would
			// scroll the session away.
		case 2:
			topic = fields[1]
		default:
			fmt.Fprintf(s.diag, "  help takes at most one topic\n")
			return false
		}
		if err := WriteHelp(topic, helpEnv{Out: s.out, IsTerminal: false}); err != nil {
			fmt.Fprintf(s.diag, "  %v\n", err)
		}
		return false
	}

	q, err := Parse(input)
	if err != nil {
		fmt.Fprintf(s.diag, "  syntax error: %v\n", err)
		return false
	}
	if err := q.ValidateExecutable(); err != nil {
		fmt.Fprintf(s.diag, "  error: %v\n", err)
		return false
	}

	results, err := SearchStanzas(q, s.stanzas, s.lineStarts)
	if err != nil {
		fmt.Fprintf(s.diag, "  error: %v\n", err)
		return false
	}

	if q.IsWrite() {
		s.executeWrite(q, results)
		return false
	}

	s.reportRead(q, results)
	return false
}

// reportRead prints the results of a read-only query as JSON.
func (s *replState) reportRead(q *Query, results []SearchResult) {
	out := make([]MatchResult, 0, len(results))
	for _, res := range results {
		if q.Action == ActionDrop {
			continue
		}
		mr := NewMatchResult(PositionStanza, res.StartLine, s.precedingLines(q, res))
		mr.StanzaIndex = res.StanzaIndex
		mr.Stanza = res.Stanza
		for token, values := range res.Matched {
			mr.MatchedTokens[token] = values
		}
		out = append(out, mr)
	}

	if q.Action == ActionDrop {
		out = s.droppedResults(results)
	}

	fmt.Fprintf(s.diag, "  %s\n", pluralize(len(out), "stanza"))
	if err := writeJSON(s.out, out); err != nil {
		fmt.Fprintf(s.diag, "  error: %v\n", err)
	}
}

// precedingLines returns the context lines a query asked for, taken from the
// session's working copy. It returns nil when no context was requested.
//
// It reads the original input rather than the stanza list so that blank
// separator lines, which the chunker drops, are still available as context.
func (s *replState) precedingLines(q *Query, res SearchResult) []string {
	size := q.ContextSize
	if !q.HasContext {
		size = 0
	}
	if size <= 0 || res.StartLine <= 1 {
		return nil
	}

	// StartLine is 1-based, so the line immediately before it is index StartLine-2.
	end := res.StartLine - 1
	start := end - size
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, end-start)
	out = append(out, s.lines[start:end]...)
	return out
}

// droppedResults builds the complement of the match set, which is what a drop
// query reports.
func (s *replState) droppedResults(results []SearchResult) []MatchResult {
	matched := make(map[int]bool, len(results))
	for _, r := range results {
		matched[r.StanzaIndex] = true
	}

	var out []MatchResult
	for i, stanza := range s.stanzas {
		if matched[i+1] {
			continue
		}
		mr := NewMatchResult(PositionStanza, s.lineOf(i), nil)
		mr.StanzaIndex = i + 1
		mr.Stanza = stanza
		out = append(out, mr)
	}
	return out
}

// executeWrite runs the replace path for a query.
func (s *replState) executeWrite(q *Query, results []SearchResult) {
	mutations, err := BuildQueryMutations(q, results, s.lines)
	if err != nil {
		fmt.Fprintf(s.diag, "  error: %v\n", err)
		return
	}
	if len(mutations) == 0 {
		fmt.Fprintf(s.diag, "  no changes\n")
		return
	}
	if s.path == "" {
		fmt.Fprintf(s.diag, "  cannot write: input is from stdin\n")
		return
	}

	plan, err := NewChangePlan(s.path, s.lines, mutations)
	if err != nil {
		fmt.Fprintf(s.diag, "  error: %v\n", err)
		return
	}

	execMode := ModeDefault
	if q.HasModifier(ModifierYolo) {
		execMode = ModeYOLO
	}
	if q.HasModifier(ModifierSpot) {
		execMode = ModeSpot
	}

	opts := ExecOptions{
		Mode:   execMode,
		SpotN:  q.SpotN,
		DryRun: q.HasModifier(ModifierDryRun) || q.HasModifier("dryrun"),
		Color:  s.color,
	}
	if opts.SpotN == 0 {
		opts.SpotN = 5
	}

	result, err := Execute(plan, opts, s.in, s.diag)
	if err != nil {
		fmt.Fprintf(s.diag, "  error: %v\n", err)
		return
	}
	if err := WriteExecResult(s.out, result); err != nil {
		fmt.Fprintf(s.diag, "  error: %v\n", err)
	}

	// Refresh the working copy so subsequent queries see the new content.
	if result.Status == StatusApplied {
		if err := s.reload(); err != nil {
			fmt.Fprintf(s.diag, "  warning: could not reload %s: %v\n", s.path, err)
		}
	}
}

// reload re-reads the working file after a successful write.
func (s *replState) reload() error {
	src, err := OpenInput(s.path)
	if err != nil {
		return err
	}
	defer src.Close()

	lines, err := readAllLines(src)
	if err != nil {
		return err
	}
	s.lines = lines
	return s.rechunk()
}

// printTokens lists the registered semantic tokens.
func (s *replState) printTokens() {
	for _, name := range registry.Names() {
		fmt.Fprintf(s.out, "  %-14s %s\n", name, registry.Describe(name))
	}
}

// --- completion -----------------------------------------------------------

// completer is the context-aware TAB completion function for the REPL.
//
// It reads only the words before the cursor and decides what belongs next:
// actions at the start, then a scope, then targets, then modifiers. Suggestions
// carry descriptions so the completion menu explains itself.
func completer(d prompt.Document) ([]prompt.Suggest, istrings.RuneNumber, istrings.RuneNumber) {
	before := d.TextBeforeCursor()

	wordStart := strings.LastIndexByte(before, ' ') + 1
	word := before[wordStart:]

	// The replacement span: from the start of the word being typed to the cursor.
	startRune := istrings.RuneNumber(utf8.RuneCountInString(before[:wordStart]))
	endRune := istrings.RuneNumber(utf8.RuneCountInString(before))

	suggestions := suggestFor(before[:wordStart])

	// A bracketed target list completes token names individually.
	if inBracketList(before) {
		suggestions = tokenSuggestions()
	}

	return prompt.FilterHasPrefix(suggestions, word, true), startRune, endRune
}

// suggestFor returns the suggestions appropriate to what has already been typed.
func suggestFor(prefix string) []prompt.Suggest {
	fields := strings.Fields(prefix)

	// Nothing typed yet: offer actions.
	if len(fields) == 0 {
		return actionSuggestions()
	}

	// "help <topic>" completes reference topics rather than grammar.
	if strings.EqualFold(fields[0], "help") || fields[0] == "?" {
		if len(fields) == 1 {
			return helpTopicSuggestions()
		}
		return nil
	}

	action := strings.ToLower(fields[0])

	// After "replace", a bare target means a semantic token name that will be
	// rewritten wholesale; after a scope, targets are token names too.
	hasTargets := false
	expectValue := false
	groupAwaitingField := false

	for i := 1; i < len(fields); i++ {
		f := strings.ToLower(fields[i])
		switch {
		case f == "with":
			expectValue = true
		case f == "group":
			groupAwaitingField = true
		case groupAwaitingField && f == "by":
			groupAwaitingField = false
		case isScopeWord(f):
			// A scope narrows the match set; it is not a target.
		case strings.HasPrefix(fields[i], "["):
			hasTargets = true
		case isModifierKeyword(f):
			// keep scanning
		case expectValue:
			expectValue = false
		default:
			hasTargets = true
		}
	}

	if expectValue {
		// After "with" the next word is either the "context" keyword or a quoted
		// value. Only the keyword is worth completing; a value is free text.
		return []prompt.Suggest{
			{Text: contextKeyword, Description: "context - embed the N preceding lines, e.g. with context 5"},
		}
	}

	if !hasTargets {
		// After an action and before any target, both a scope ("get first 3
		// [ip]") and a target ("get [ip]") are valid, so offer both. A replace
		// needs a "with" clause, so nudge toward that instead.
		if action == ActionReplace {
			return append(tokenSuggestions(), modifierSuggestions()...)
		}
		return append(tokenSuggestions(), scopeSuggestions()...)
	}

	// Targets are present, so only modifiers can follow.
	return modifierSuggestions()
}

// inBracketList reports whether the cursor sits inside an unclosed [ ... ].
func inBracketList(text string) bool {
	open := strings.Count(text, "[")
	closed := strings.Count(text, "]")
	return open > closed
}

// actionSuggestions lists the grammar actions.
func actionSuggestions() []prompt.Suggest {
	return []prompt.Suggest{
		{Text: ActionGet, Description: "get    - report matching stanzas (default)"},
		{Text: ActionKeep, Description: "keep   - report matching stanzas only"},
		{Text: ActionDrop, Description: "drop   - report non-matching stanzas"},
		{Text: ActionReplace, Description: "replace- rewrite matching text"},
	}
}

// scopeSuggestions lists the grammar scopes.
func scopeSuggestions() []prompt.Suggest {
	return []prompt.Suggest{
		{Text: ScopeAll, Description: "all    - every stanza"},
		{Text: ScopeFirstN, Description: "first  - first N matches"},
		{Text: ScopeLastN, Description: "last   - last N matches"},
		{Text: ScopeToday, Description: "today  - stanzas dated today"},
		{Text: ScopeStanza, Description: "stanza - one stanza by index"},
	}
}

// tokenSuggestions lists every registered token with its description.
func tokenSuggestions() []prompt.Suggest {
	names := registry.Names()
	out := make([]prompt.Suggest, 0, len(names))
	for _, name := range names {
		out = append(out, prompt.Suggest{Text: name, Description: registry.Describe(name)})
	}
	return out
}

// modifierSuggestions lists the execution modifiers.
func modifierSuggestions() []prompt.Suggest {
	return []prompt.Suggest{
		{Text: "with", Description: "with   - replacement value or context, e.g. with context 5"},
		{Text: ModifierYolo, Description: "yolo   - write without prompting"},
		{Text: ModifierSpot, Description: "spot   - show N random diffs, then confirm"},
		{Text: ModifierDryRun, Description: "dryrun - show the diff, never write"},
		{Text: ModifierNoColor, Description: "no-color - plain diff output"},
		{Text: ModifierRequireAll, Description: "require  - require every target to match, e.g. require all"},
	}
}

// helpTopicSuggestions lists the reference topics with their descriptions.
func helpTopicSuggestions() []prompt.Suggest {
	out := make([]prompt.Suggest, 0, len(helpTopics)+1)
	for _, t := range helpTopics {
		out = append(out, prompt.Suggest{Text: t, Description: t + " - " + helpTopicDescriptions[t]})
	}
	out = append(out, prompt.Suggest{Text: "all", Description: "all - the complete manual"})
	return out
}

// isScopeWord reports whether w names a scope.
func isScopeWord(w string) bool {
	for _, s := range ScopeNames {
		if w == s {
			return true
		}
	}
	return false
}

// TokenCompletions returns the token names as a sorted list. It exists for
// tests and for callers that want completion data without a Document.
func TokenCompletions() []string {
	names := registry.Names()
	sort.Strings(names)
	return names
}
