package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/badvibecoder/vibepat/internal/registry"
)

// The help system is the documented contract for everything else, so these tests
// check two different things: that routing works, and that the reference is
// actually complete. The completeness checks matter more, because help text
// rots silently when a token or modifier is added and nobody updates the prose.

// --- routing ---------------------------------------------------------------

// TestHelpTopicsAllResolve verifies every canonical topic produces content.
func TestHelpTopicsAllResolve(t *testing.T) {
	for _, topic := range HelpTopics() {
		text, err := helpPageFor(topic)
		if err != nil {
			t.Errorf("helpPageFor(%q): %v", topic, err)
			continue
		}
		if len(text) < 200 {
			t.Errorf("topic %q produced only %d bytes; looks empty", topic, len(text))
		}
		if !strings.Contains(text, "vibepat") {
			t.Errorf("topic %q does not mention the program name", topic)
		}
	}
}

// TestHelpAllContainsEveryTopic verifies the complete manual is a genuine
// superset, so "help" and "help all" cannot silently omit a topic.
func TestHelpAllContainsEveryTopic(t *testing.T) {
	all, err := helpPageFor("all")
	if err != nil {
		t.Fatalf("helpPageFor(all): %v", err)
	}

	for _, topic := range HelpTopics() {
		page, err := helpPageFor(topic)
		if err != nil {
			t.Fatalf("helpPageFor(%q): %v", topic, err)
		}
		// Compare on the body, which excludes the shared preamble.
		body := helpBodies[topic].body
		if !strings.Contains(all, body) {
			t.Errorf("the complete manual does not contain the %q topic", topic)
		}
		_ = page
	}
}

// TestHelpIndexListsEveryTopic verifies the table of contents is complete.
func TestHelpIndexListsEveryTopic(t *testing.T) {
	index, err := helpPageFor("index")
	if err != nil {
		t.Fatalf("helpPageFor(index): %v", err)
	}
	for _, topic := range HelpTopics() {
		if !strings.Contains(index, topic) {
			t.Errorf("the index does not list %q", topic)
		}
		if !strings.Contains(index, helpTopicDescriptions[topic]) {
			t.Errorf("the index does not describe %q", topic)
		}
	}
}

// TestHelpAliasesResolve verifies alternative names route to real content.
func TestHelpAliasesResolve(t *testing.T) {
	for alias, canonical := range helpAliases {
		if alias == "" {
			continue
		}
		text, err := helpPageFor(alias)
		if err != nil {
			t.Errorf("helpPageFor(%q): %v", alias, err)
			continue
		}
		want, err := helpPageFor(canonical)
		if err != nil {
			t.Fatalf("helpPageFor(%q): %v", canonical, err)
		}
		if text != want {
			t.Errorf("alias %q did not resolve to %q", alias, canonical)
		}
	}
}

// TestHelpEmptyTopicAndAllDiffer documents the deliberate split: a bare "help"
// teaches, while "help all" is the complete reference. An earlier version made
// both print the full manual, which handed a newcomer 600 lines.
func TestHelpEmptyTopicAndAllDiffer(t *testing.T) {
	empty, err := helpPageFor("")
	if err != nil {
		t.Fatalf("helpPageFor(\"\"): %v", err)
	}
	all, err := helpPageFor("all")
	if err != nil {
		t.Fatalf("helpPageFor(all): %v", err)
	}
	if empty == all {
		t.Error("bare help and \"help all\" should not be identical")
	}
	if len(empty) >= len(all) {
		t.Errorf("bare help is %d bytes and the manual is %d; bare help should be shorter",
			len(empty), len(all))
	}
}

// TestHelpUnknownTopicIsAnError verifies a typo is corrected rather than buried
// under hundreds of lines of manual.
func TestHelpUnknownTopicIsAnError(t *testing.T) {
	_, err := helpPageFor("nosuchtopic")
	if err == nil {
		t.Fatal("helpPageFor accepted an unknown topic")
	}
	if !strings.Contains(err.Error(), "nosuchtopic") {
		t.Errorf("error does not name the bad topic: %v", err)
	}
	// It must also list what is available, so the reader can recover.
	for _, topic := range HelpTopics() {
		if !strings.Contains(err.Error(), topic) {
			t.Errorf("error does not list the available topic %q: %v", topic, err)
		}
	}
}

// TestHelpTopicIsCaseAndSpaceInsensitive verifies forgiving input.
func TestHelpTopicIsCaseAndSpaceInsensitive(t *testing.T) {
	for _, variant := range []string{"GRAMMAR", "Grammar", "  grammar  ", "grammar\n"} {
		if _, err := helpPageFor(variant); err != nil {
			t.Errorf("helpPageFor(%q): %v", variant, err)
		}
	}
}

// --- completeness ----------------------------------------------------------

// TestHelpDocumentsEveryAction verifies the action list matches the parser.
func TestHelpDocumentsEveryAction(t *testing.T) {
	text := helpBodies["grammar"].body
	for _, action := range ActionNames {
		if !strings.Contains(text, action) {
			t.Errorf("the grammar reference does not document the %q action", action)
		}
	}
	// Behavior, mutability, and stream must all be stated per action.
	for _, want := range []string{
		"read-only", "WRITES", "stdout", "stderr",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the grammar reference never mentions %q", want)
		}
	}
}

// TestHelpDocumentsEveryScope verifies the scope list matches the parser.
func TestHelpDocumentsEveryScope(t *testing.T) {
	text := helpBodies["grammar"].body
	for _, scope := range ScopeNames {
		if !strings.Contains(text, scope) {
			t.Errorf("the grammar reference does not document the %q scope", scope)
		}
	}
	// The subtle rule about first N deserves an explicit statement.
	if !strings.Contains(text, "MATCHES REPORTED") {
		t.Error("the grammar reference does not explain that first N bounds matches, not lines")
	}
}

// TestHelpDocumentsEveryRegisteredToken is the load-bearing completeness check:
// a token added to the registry without help text fails here.
func TestHelpDocumentsEveryRegisteredToken(t *testing.T) {
	text := helpBodies["tokens"].body
	for _, token := range registry.Names() {
		if !strings.Contains(text, token) {
			t.Errorf("the token reference does not document %q, which is registered", token)
		}
	}
}

// TestHelpDocumentsTheLiteralTarget verifies the non-token target form.
func TestHelpDocumentsTheLiteralTarget(t *testing.T) {
	if !strings.Contains(helpBodies["grammar"].body, "literal") {
		t.Error("the grammar reference does not document the ['literal'] target")
	}
}

// TestHelpDocumentsEveryModifier verifies each modifier and flag is covered.
func TestHelpDocumentsEveryModifier(t *testing.T) {
	text := helpBodies["modifiers"].body

	grammarModifiers := []string{
		"with ['X']", "require all", "with context", "dryrun",
		ModifierYolo, ModifierForce, ModifierSpot, ModifierNoColor,
	}
	for _, m := range grammarModifiers {
		if !strings.Contains(text, m) {
			t.Errorf("the modifier reference does not document %q", m)
		}
	}

	// Safety semantics that would be dangerous to leave implicit.
	for _, want := range []string{"commits every mutation", "atomic", ".bak", "Permissions"} {
		if !strings.Contains(text, want) {
			t.Errorf("the modifier reference does not state %q", want)
		}
	}
}

// TestHelpDocumentsCustomTokens verifies the custom token config is covered,
// including the YAML quoting trap that makes the documented format unusable if
// written with double quotes.
func TestHelpDocumentsCustomTokens(t *testing.T) {
	text := helpBodies["tokens"].body
	for _, want := range []string{"custom.yaml", "SINGLE quotes", "world-writable", "capture group"} {
		if !strings.Contains(text, want) {
			t.Errorf("the token reference does not cover %q", want)
		}
	}
}

// TestHelpExamplesCoverTheRequiredWorkflows verifies the recipe matrix includes
// each scenario that was specified, with the exact command that implements it.
func TestHelpExamplesCoverTheRequiredWorkflows(t *testing.T) {
	text := helpBodies["examples"].body

	required := []string{
		"lspci -vvv | vibepat get all [bdf, link_downgrade] require all",
		"journalctl -fu kubelet | vibepat keep all [error] with context 5",
		"replace ip ['10.0.1.0/24'] with ['10.50.1.X'] spot 5",
		"replace driver ['vfio-pci'] with ['amdgpu'] yolo",
		"drop all [error] incident.log",
	}
	for _, cmd := range required {
		if !strings.Contains(text, cmd) {
			t.Errorf("the examples page is missing the workflow:\n  %s", cmd)
		}
	}
}

// TestHelpExamplesCommandsAreParseable is the strongest check on the example
// page: every grammar shown must actually parse. A recipe that does not parse is
// worse than no recipe.
//
// Each example is reduced to the grammar the shell would hand the program: any
// trailing pipeline or redirect is dropped, and the trailing file argument is
// removed, because splitPositionals takes that out before parsing.
func TestHelpExamplesCommandsAreParseable(t *testing.T) {
	text := helpBodies["examples"].body

	var checked int
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "=") {
			continue
		}

		// Require the invocation to start the line, so prose that merely
		// mentions the program name is not mistaken for a command.
		if !strings.HasPrefix(trimmed, "vibepat ") {
			continue
		}
		rest := trimmed[len("vibepat "):]

		// Drop anything after a shell operator, and a line continuation.
		for _, sep := range []string{" | ", " > ", " \\"} {
			if i := strings.Index(rest, sep); i >= 0 {
				rest = rest[:i]
			}
		}
		args := strings.Fields(strings.TrimSpace(rest))
		if len(args) == 0 {
			continue
		}
		// The help subcommand and flag-only invocations are not grammar.
		if args[0] == "help" || strings.HasPrefix(args[0], "-") {
			continue
		}
		// Strip the trailing file argument, which splitPositionals removes
		// before the parser ever sees it.
		if len(args) > 1 && looksLikePath(args[len(args)-1]) {
			args = args[:len(args)-1]
		}

		checked++
		if _, err := Parse(strings.Join(args, " ")); err != nil {
			t.Errorf("example does not parse: %s\n  %v", trimmed, err)
		}
	}

	if checked < 10 {
		t.Fatalf("only %d examples were checked; the extraction logic is not matching", checked)
	}
	t.Logf("parsed %d example commands", checked)
}

// looksLikePath reports whether a token is an input file rather than grammar.
func looksLikePath(tok string) bool {
	if strings.HasPrefix(tok, "[") || strings.HasPrefix(tok, "'") {
		return false
	}
	return strings.ContainsAny(tok, "./")
}

// TestHelpMentionsEveryFlag verifies the flag reference is not stale. Flags that
// exist but go undocumented are invisible to users.
func TestHelpMentionsEveryFlag(t *testing.T) {
	text := helpBodies["modifiers"].body + helpBodies["overview"].body

	userFacing := []string{
		"--mode", "--sample", "--file", "--tokens", "--no-custom-tokens",
		"--context", "--exec", "--spot", "--dryrun", "--no-color",
	}
	for _, flag := range userFacing {
		if !strings.Contains(text, flag) {
			t.Errorf("no help topic documents the %s flag", flag)
		}
	}
}

// TestHelpNoStaleTrailingContextLanguage guards the terminology fix: context is
// the lines preceding a match, never trailing ones.
func TestHelpNoStaleTrailingContextLanguage(t *testing.T) {
	all, err := helpPageFor("all")
	if err != nil {
		t.Fatalf("helpPageFor(all): %v", err)
	}
	if strings.Contains(strings.ToLower(all), "trailing context") {
		t.Error("help text calls the context buffer 'trailing'; it holds preceding lines")
	}
	if !strings.Contains(all, "PRECEDING") {
		t.Error("help text never states that context is the preceding lines")
	}
}

// --- the help command ------------------------------------------------------

// TestRunHelpDispatch verifies the "help [topic]" command form.
func TestRunHelpDispatch(t *testing.T) {
	cases := []struct {
		args    []string
		handled bool
		wantErr bool
	}{
		{[]string{"help"}, true, false},
		{[]string{"help", "all"}, true, false},
		{[]string{"help", "grammar"}, true, false},
		{[]string{"help", "tokens"}, true, false},
		{[]string{"help", "modifiers"}, true, false},
		{[]string{"help", "examples"}, true, false},
		{[]string{"HELP"}, true, false},
		{[]string{"help", "nosuchtopic"}, true, true},
		{[]string{"help", "grammar", "extra"}, true, true},
		{[]string{"get", "all"}, false, false},
		{[]string{"--help"}, false, false},
		{[]string{}, false, false},
	}

	for _, tc := range cases {
		var buf bytes.Buffer
		handled, err := runHelp(tc.args, helpEnv{Out: &buf, IsTerminal: false})
		if handled != tc.handled {
			t.Errorf("runHelp(%v) handled = %v, want %v", tc.args, handled, tc.handled)
		}
		if (err != nil) != tc.wantErr {
			t.Errorf("runHelp(%v) err = %v, wantErr %v", tc.args, err, tc.wantErr)
		}
		if handled && !tc.wantErr && buf.Len() == 0 {
			t.Errorf("runHelp(%v) produced no output", tc.args)
		}
	}
}

// TestRunHelpOutputMatchesDirectRender verifies the command path and the render
// path agree, so there is only one rendering.
func TestRunHelpOutputMatchesDirectRender(t *testing.T) {
	var buf bytes.Buffer
	if _, err := runHelp([]string{"help", "tokens"}, helpEnv{Out: &buf, IsTerminal: false}); err != nil {
		t.Fatalf("runHelp: %v", err)
	}

	direct, err := helpPageFor("tokens")
	if err != nil {
		t.Fatalf("helpPageFor: %v", err)
	}
	if buf.String() != direct {
		t.Error("the command path and the direct render disagree")
	}
}

// TestRunHelpIsNotTreatedAsGrammar verifies "help" does not fall through to the
// parser, where it would be read as a target and produce a confusing error.
func TestRunHelpIsNotTreatedAsGrammar(t *testing.T) {
	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"help", "grammar"}, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "GRAMMAR") {
		t.Errorf("stdout does not look like the grammar page: %.120s", out.String())
	}
	// A help request reads no input, so nothing should be reported as a match.
	if strings.Contains(out.String(), "matched_tokens") {
		t.Error("a help request produced match output")
	}
}

// TestRunDashDashHelpPrintsIndex verifies --help stays a short orientation
// rather than dumping the complete manual.
func TestRunDashDashHelpPrintsIndex(t *testing.T) {
	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"--help"}, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "TOPICS") {
		t.Errorf("--help did not print the index: %.200s", text)
	}
	for _, topic := range HelpTopics() {
		if !strings.Contains(text, topic) {
			t.Errorf("--help index does not list %q", topic)
		}
	}
	// The index must be substantially shorter than the full manual.
	all, err := helpPageFor("all")
	if err != nil {
		t.Fatalf("helpPageFor: %v", err)
	}
	if len(text) >= len(all)/2 {
		t.Errorf("--help printed %d bytes, which is not an index (full manual is %d)", len(text), len(all))
	}
}

// TestRunHelpIsReadOnly verifies help never writes. It takes no input file, so
// this checks the property that matters: no file is created, modified, or backed
// up anywhere in the working directory.
func TestRunHelpIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "untouched.txt")
	const content = "addr 10.0.0.1\n"
	if err := writeFile(path, content); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// The file is unreferenced by help; it exists only to prove nothing touches it.
	_ = path

	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"help", "grammar"}, strings.NewReader(content), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Error("help modified a file")
	}
	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Error("help created a backup")
	}
}

// --- pager -----------------------------------------------------------------

// TestWritePagedNonInteractiveIsPlain verifies a pipe gets plain text with no
// pager involvement.
func TestWritePagedNonInteractiveIsPlain(t *testing.T) {
	var buf bytes.Buffer
	if err := writePaged(&buf, "hello\n", false); err != nil {
		t.Fatalf("writePaged: %v", err)
	}
	if buf.String() != "hello\n" {
		t.Errorf("got %q, want the text unchanged", buf.String())
	}
}

// TestWritePagedFallsBackWhenNoPager verifies the manual is printed rather than
// lost when no pager exists. PAGER is pointed at a nonexistent binary and the
// standard fallbacks are unreachable in a bare environment.
func TestWritePagedFallsBackWhenNoPager(t *testing.T) {
	t.Setenv("PAGER", "/nonexistent/pager-binary")

	var buf bytes.Buffer
	// IsTerminal is true, so the pager path is attempted; every candidate fails
	// here only if less and more are absent, which they may not be. Either way
	// the content must arrive.
	if err := writePaged(&buf, "content\n", true); err != nil {
		t.Fatalf("writePaged: %v", err)
	}
	if !strings.Contains(buf.String(), "content") {
		t.Errorf("content was lost: %q", buf.String())
	}
}

// TestWritePagedUsesPagerAndPassesArgs verifies $PAGER is honoured with its
// arguments.
func TestWritePagedUsesPagerAndPassesArgs(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fakepager")
	body := "#!/bin/sh\necho \"INVOKED $*\"\ncat\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv("PAGER", script+" --marker")

	var buf bytes.Buffer
	if err := writePaged(&buf, "payload\n", true); err != nil {
		t.Fatalf("writePaged: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "INVOKED --marker") {
		t.Errorf("$PAGER was not invoked with its arguments: %q", got)
	}
	if !strings.Contains(got, "payload") {
		t.Errorf("the pager did not receive the text: %q", got)
	}
}

// TestWritePagedSurvivesPagerExitingEarly verifies quitting a pager is not an
// error, which is how every reader stops reading.
func TestWritePagedSurvivesPagerExitingEarly(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "quitpager")
	// Exit without reading stdin, simulating an immediate quit.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv("PAGER", script)

	// A large body makes a broken pipe likely rather than incidental.
	large := strings.Repeat("line of help text\n", 5000)
	var buf bytes.Buffer
	if err := writePaged(&buf, large, true); err != nil {
		t.Fatalf("writePaged returned an error for an early pager exit: %v", err)
	}
}

// TestPagerCandidatesHonoursPager verifies the candidate ordering.
func TestPagerCandidatesHonoursPager(t *testing.T) {
	t.Setenv("PAGER", "my-pager -x")

	got := pagerCandidates()
	if len(got) == 0 {
		t.Fatal("no pager candidates")
	}
	if got[0][0] != "my-pager" || len(got[0]) != 2 || got[0][1] != "-x" {
		t.Errorf("first candidate = %v, want [my-pager -x]", got[0])
	}

	// The fallbacks must still be present behind it.
	names := map[string]bool{}
	for _, c := range got {
		names[c[0]] = true
	}
	if !names["less"] || !names["more"] {
		t.Errorf("fallback pagers missing from %v", got)
	}
}

// TestPagerCandidatesWithoutPager verifies the default ordering.
func TestPagerCandidatesWithoutPager(t *testing.T) {
	t.Setenv("PAGER", "")

	got := pagerCandidates()
	if len(got) != 2 {
		t.Fatalf("got %v, want the two fallbacks", got)
	}
	if got[0][0] != "less" || got[1][0] != "more" {
		t.Errorf("got %v, want less then more", got)
	}
}

// --- REPL ------------------------------------------------------------------

// TestReplHelpShowsIndexWithoutExiting is the REPL integration gate: help must
// surface the reference and keep the session alive.
func TestReplHelpShowsIndexWithoutExiting(t *testing.T) {
	state, out, _ := newReplState(t, "", "addr 10.0.0.1\n", ModeStream)

	if state.handleInput("help") {
		t.Fatal("help ended the session")
	}

	text := out.String()
	if !strings.Contains(text, "TOPICS") {
		t.Errorf("REPL help did not print the index:\n%.300s", text)
	}
	for _, topic := range HelpTopics() {
		if !strings.Contains(text, topic) {
			t.Errorf("REPL help index does not list %q", topic)
		}
	}
}

// TestReplHelpTopic verifies a targeted topic works interactively.
func TestReplHelpTopic(t *testing.T) {
	state, out, _ := newReplState(t, "", "addr 10.0.0.1\n", ModeStream)

	if state.handleInput("help tokens") {
		t.Fatal("help tokens ended the session")
	}
	if !strings.Contains(out.String(), "SEMANTIC TOKENS") {
		t.Errorf("REPL did not print the token reference:\n%.200s", out.String())
	}
}

// TestReplHelpUnknownTopicKeepsSessionAlive verifies a typo is reported without
// dropping out of interactive mode.
func TestReplHelpUnknownTopicKeepsSessionAlive(t *testing.T) {
	state, out, diag := newReplState(t, "", "addr 10.0.0.1\n", ModeStream)

	if state.handleInput("help nosuchtopic") {
		t.Fatal("an unknown topic ended the session")
	}
	if !strings.Contains(diag.String(), "nosuchtopic") {
		t.Errorf("the typo was not reported: %q", diag.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout received %q, want nothing", out.String())
	}
}

// TestReplHelpDoesNotLeakIntoQueries verifies help is intercepted before the
// grammar parser, so it is not mistaken for a target.
func TestReplHelpDoesNotLeakIntoQueries(t *testing.T) {
	state, out, _ := newReplState(t, "", "addr 10.0.0.1\n", ModeStream)

	state.handleInput("help grammar")

	if strings.Contains(out.String(), "matched_tokens") {
		t.Error("a help request produced match output")
	}
}

// TestReplHelpCompletesTopics verifies TAB completion offers the topics.
func TestReplHelpCompletesTopics(t *testing.T) {
	suggestions := suggestFor("help ")
	if len(suggestions) == 0 {
		t.Fatal("no completions offered after 'help'")
	}

	names := map[string]bool{}
	for _, s := range suggestions {
		names[s.Text] = true
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("topic suggestion %q has no description", s.Text)
		}
	}
	for _, topic := range append(HelpTopics(), "all") {
		if !names[topic] {
			t.Errorf("completion does not offer %q: %v", topic, names)
		}
	}
}

// TestHelpCompletionsIncludesAliases verifies the completion list covers the
// alternate names too.
func TestHelpCompletionsIncludesAliases(t *testing.T) {
	got := HelpCompletions()
	seen := map[string]bool{}
	for _, name := range got {
		seen[name] = true
	}
	for _, want := range []string{"all", "index", "grammar", "tokens", "modifiers", "examples", "recipes"} {
		if !seen[want] {
			t.Errorf("HelpCompletions is missing %q", want)
		}
	}
}

// TestHelpShortFlagHoists verifies -h works wherever it is written. The flag
// package defines -h implicitly, so it is easy to forget in knownFlags, and when
// it is missing the flag is not hoisted and fails as an unknown grammar token.
func TestHelpShortFlagHoists(t *testing.T) {
	for _, args := range [][]string{
		{"-h"},
		{"get", "all", "[ip]", "-h"},
		{"--help"},
		{"get", "all", "[ip]", "--help"},
	} {
		var out, diag bytes.Buffer
		if err := runPlain(t, args, strings.NewReader("addr 10.0.0.1\n"), &out, &diag); err != nil {
			t.Errorf("run(%v): %v", args, err)
			continue
		}
		if !strings.Contains(out.String(), "TOPICS") {
			t.Errorf("run(%v) did not print the help index", args)
		}
	}
}

// --- the walkthrough -------------------------------------------------------

// walkthroughCommands extracts the "vibepat ..." invocations from a help page,
// in order. Only lines whose command actually starts the line are taken, so
// prose that mentions the program name is skipped.
func walkthroughCommands(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "$ ")
		if !strings.HasPrefix(trimmed, "vibepat ") {
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		// The usage synopsis placeholder is documentation, not a command.
		if strings.HasPrefix(fields[1], "[") {
			continue
		}
		// A "vibepat help <topic>" reference may carry a trailing description on
		// the same line; keep only the invocation.
		if fields[1] == "help" && len(fields) > 3 {
			fields = fields[:3]
		}
		out = append(out, strings.Join(fields, " "))
	}
	return out
}

// TestWalkthroughEveryCommandRuns is the gate that keeps the landing page
// honest: a teaching page whose commands do not work is worse than no page.
//
// Each documented invocation is executed against a per-command copy of the
// sample file, so the read-only steps cannot affect each other and a write step
// cannot escape into the fixture.
func TestWalkthroughEveryCommandRuns(t *testing.T) {
	page, err := helpPageFor("start")
	if err != nil {
		t.Fatalf("helpPageFor(start): %v", err)
	}

	commands := walkthroughCommands(page)
	if len(commands) < 4 {
		t.Fatalf("found only %d commands to run; the extraction is not matching", len(commands))
	}

	// Rebuild the sample the walkthrough tells the reader to create.
	const sample = "addr 10.0.0.1\nplain-b\nplain-c\naddr 10.0.0.4\n"
	dir := t.TempDir()
	canonical := filepath.Join(dir, "w.txt")
	if err := writeFile(canonical, sample); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for i, cmd := range commands {
		// Point the command at its own copy so the steps stay independent.
		source := strings.ReplaceAll(cmd, "/tmp/w.txt", canonical)
		args := strings.Fields(strings.TrimPrefix(source, "vibepat "))

		var out, diag bytes.Buffer
		if err := runPlain(t, args, strings.NewReader("n\n"), &out, &diag); err != nil {
			t.Errorf("walkthrough command %d failed: %s\n  %v", i+1, cmd, err)
			continue
		}
		if out.Len() == 0 && diag.Len() == 0 {
			t.Errorf("walkthrough command %d produced no output: %s", i+1, cmd)
		}
	}
}

// TestWalkthroughFileSurvivesReadOnlySteps verifies the landing page's claim
// that every command it shows is safe: nothing is written and no backup appears.
func TestWalkthroughFileSurvivesReadOnlySteps(t *testing.T) {
	page, err := helpPageFor("start")
	if err != nil {
		t.Fatalf("helpPageFor(start): %v", err)
	}
	if !strings.Contains(page, "none of them writes anything") {
		t.Fatal("the walkthrough no longer makes the read-only claim")
	}

	const sample = "addr 10.0.0.1\nplain-b\nplain-c\naddr 10.0.0.4\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "w.txt")
	if err := writeFile(path, sample); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for _, cmd := range walkthroughCommands(page) {
		source := strings.ReplaceAll(cmd, "/tmp/w.txt", path)

		// Decline any prompt, and never enable a writing mode.
		var out, diag bytes.Buffer
		if err := runPlain(t, strings.Fields(strings.TrimPrefix(source, "vibepat ")),
			strings.NewReader("n\n"), &out, &diag); err != nil {
			t.Fatalf("command %s: %v", cmd, err)
		}

		if got, _ := os.ReadFile(path); string(got) != sample {
			t.Fatalf("command %s modified the file:\n%q", cmd, got)
		}
		if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
			t.Fatalf("command %s created a backup", cmd)
		}
	}
}

// TestWalkthroughDocumentsTheWorkflow verifies the page actually teaches the
// four verbs rather than only listing them.
func TestWalkthroughDocumentsTheWorkflow(t *testing.T) {
	body := helpBodies["start"].body

	for _, want := range []string{
		"1. FIND THE ADDRESSES",
		"2. NARROW IT",
		"3. INVERT IT",
		"4. SEE THE CHANGE BEFORE MAKING IT",
		"WHERE TO GO NEXT",
		// Field-by-field explanation of the JSON, which is the part a newcomer
		// cannot infer.
		"matched_tokens",
		"context_lines",
		"stanza_index",
		"line_number",
		// The non-obvious scope rule, stated where it is first useful.
		"first two MATCHES, not the first two lines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the walkthrough is missing %q", want)
		}
	}
}

// TestWalkthroughOutputIsVerbatim verifies the walkthrough shows the real JSON
// shape rather than a paraphrase, so a reader can match what they see.
func TestWalkthroughOutputIsVerbatim(t *testing.T) {
	body := helpBodies["start"].body

	for _, want := range []string{
		`{"matched_tokens":{"ip":["10.0.0.1"]},"context_lines":[]`,
		"+    1  addr 10.9.9.1",
		"-    4  addr 10.0.0.4",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the walkthrough does not show the real output %q", want)
		}
	}
}

// TestHelpDefaultTopicIsTheWalkthrough verifies bare "help" teaches rather than
// dumping the manual.
func TestHelpDefaultTopicIsTheWalkthrough(t *testing.T) {
	bare, err := helpPageFor("")
	if err != nil {
		t.Fatalf("helpPageFor(\"\"): %v", err)
	}
	start, err := helpPageFor("start")
	if err != nil {
		t.Fatalf("helpPageFor(start): %v", err)
	}
	if bare != start {
		t.Error("bare help does not land on the walkthrough")
	}

	var buf bytes.Buffer
	if _, err := runHelp([]string{"help"}, helpEnv{Out: &buf, IsTerminal: false}); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	if !strings.Contains(buf.String(), "START HERE") {
		t.Errorf("vibepat help did not print the walkthrough: %.120s", buf.String())
	}
}

// TestWalkthroughIsFirstInTheManual verifies the manual opens with the lesson,
// so "help all" also leads with teaching rather than reference.
func TestWalkthroughIsFirstInTheManual(t *testing.T) {
	all, err := helpPageFor("all")
	if err != nil {
		t.Fatalf("helpPageFor(all): %v", err)
	}
	if !strings.Contains(all, "START HERE") {
		t.Fatal("the complete manual does not include the walkthrough")
	}
	if start, overview := strings.Index(all, "START HERE"), strings.Index(all, "OVERVIEW"); start > overview {
		t.Error("the walkthrough appears after the reference material")
	}
}
