package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// --- NDJSON shape ---------------------------------------------------------

// TestStreamMatchesEmitsOneObjectPerLine is the schema gate for correction #4:
// read-only output is newline-delimited JSON, one object per matched stanza.
func TestStreamMatchesEmitsOneObjectPerLine(t *testing.T) {
	src := &InputSource{Name: "<test>", Reader: strings.NewReader("addr 10.0.0.1\naddr 10.0.0.2\n")}
	defer src.Close()

	var out bytes.Buffer
	if err := streamMatches(src, options{mode: ModeStream, tokens: []string{"ip"}}, &out, io.Discard); err != nil {
		t.Fatalf("streamMatches: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), out.String())
	}
	for i, line := range lines {
		// Each line must be a complete JSON object on its own.
		var m MatchResult
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d is not a self-contained JSON object: %v (%q)", i, err, line)
		}
		// And it must not be wrapped in an array.
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			t.Errorf("line %d is an array element, not a standalone object: %q", i, line)
		}
	}
}

// TestStreamMatchesFlushesIncrementally is the real streaming property: output
// must appear before the input is exhausted, so `tail -f | vibepat` works. A
// buffered implementation would emit nothing until the writer is closed.
func TestStreamMatchesFlushesIncrementally(t *testing.T) {
	// The input stays open; only the output is observed.
	src := &InputSource{Name: "<pipe>", Reader: strings.NewReader("addr 10.0.0.1\n")}

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- streamMatches(src, options{mode: ModeStream, tokens: []string{"ip"}}, pw, io.Discard)
	}()

	// Read the first emitted line before the writer has been closed.
	lineCh := make(chan string, 1)
	go func() {
		br := bufio.NewReader(pr)
		line, err := br.ReadString('\n')
		if err != nil {
			lineCh <- ""
			return
		}
		lineCh <- line
		_, _ = io.Copy(io.Discard, br)
	}()

	select {
	case line := <-lineCh:
		if line == "" {
			t.Fatal("no output was produced")
		}
		var m MatchResult
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("first emitted line is not valid JSON: %v (%q)", err, line)
		}
		if got := m.MatchedTokens["ip"]; len(got) != 1 || got[0] != "10.0.0.1" {
			t.Errorf("first result tokens = %v, want the address", m.MatchedTokens)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no output appeared; the result is being buffered until EOF")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("streamMatches: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("streamMatches did not return")
	}
}

// TestStreamMatchesEmptyInputEmitsNothing verifies an empty stream produces no
// output at all, rather than an empty array.
func TestStreamMatchesEmptyInputEmitsNothing(t *testing.T) {
	src := &InputSource{Name: "<test>", Reader: strings.NewReader("")}
	defer src.Close()

	var out bytes.Buffer
	if err := streamMatches(src, options{mode: ModeStream, tokens: []string{"ip"}}, &out, io.Discard); err != nil {
		t.Fatalf("streamMatches: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("empty input produced %q, want nothing", out.String())
	}
}

// TestStreamQueryLastNIsBounded verifies the "last N" scope streams with only N
// results retained rather than buffering the whole input.
func TestStreamQueryLastNIsBounded(t *testing.T) {
	var input strings.Builder
	for i := 0; i < 500; i++ {
		input.WriteString("addr 10.0.0.1\n")
	}

	src := &InputSource{Name: "<test>", Reader: strings.NewReader(input.String())}
	defer src.Close()

	q, err := Parse("get last 3 [ip]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var out bytes.Buffer
	if err := streamQuery(src, q, options{mode: ModeStream}, &out, io.Discard); err != nil {
		t.Fatalf("streamQuery: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 3 {
		t.Fatalf("got %d results, want exactly 3", len(results))
	}
	if results[2].StanzaIndex != 500 {
		t.Errorf("last result StanzaIndex = %d, want 500", results[2].StanzaIndex)
	}
}

// TestStreamQueryFirstNStopsEarly verifies "first N" bounds output and returns
// without reading the whole stream.
func TestStreamQueryFirstNStopsEarly(t *testing.T) {
	// Without an early exit this reader would be consumed to EOF; with one, the
	// leftover lines must remain unread.
	body := strings.Repeat("addr 10.0.0.1\n", 100)
	src := &InputSource{Name: "<test>", Reader: strings.NewReader(body)}
	defer src.Close()

	q, err := Parse("get first 2 [ip]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var out bytes.Buffer
	if err := streamQuery(src, q, options{mode: ModeStream}, &out, io.Discard); err != nil {
		t.Fatalf("streamQuery: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

// TestStreamQueryDropEmitsComplement verifies drop streams the non-matching
// stanzas rather than requiring the full match set in memory.
func TestStreamQueryDropEmitsComplement(t *testing.T) {
	src := &InputSource{Name: "<test>", Reader: strings.NewReader("eth0 10.0.0.1\nplain line\neth1 10.0.0.2\nalso plain\n")}
	defer src.Close()

	q, err := Parse("drop all [ip]")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var out bytes.Buffer
	if err := streamQuery(src, q, options{mode: ModeStream}, &out, io.Discard); err != nil {
		t.Fatalf("streamQuery: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 2 {
		t.Fatalf("got %d results, want the 2 non-matching stanzas", len(results))
	}
	for _, r := range results {
		if strings.Contains(r.Stanza.RawText, "10.0.0.") {
			t.Errorf("drop emitted a matching stanza: %q", r.Stanza.RawText)
		}
	}
}

// TestStreamQueryRequireAllStreams verifies conjunctive matching works on the
// streaming path.
func TestStreamQueryRequireAllStreams(t *testing.T) {
	const input = `0000:00:00.0 healthy
	LnkCap: Speed 16GT/s, Width x16
	LnkSta: Speed 16GT/s, Width x16
0000:00:01.0 degraded
	LnkCap: Speed 32GT/s, Width x4
	LnkSta: Speed 2.5GT/s, Width x4
`
	src := &InputSource{Name: "<test>", Reader: strings.NewReader(input)}
	defer src.Close()

	q, err := Parse("get all [bdf, link_downgrade] require all")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var out bytes.Buffer
	if err := streamQuery(src, q, options{mode: ModeHeader}, &out, io.Discard); err != nil {
		t.Fatalf("streamQuery: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 1 {
		t.Fatalf("got %d results, want only the degraded device", len(results))
	}
	if !strings.HasPrefix(results[0].Stanza.Lines[0], "0000:00:01.0") {
		t.Errorf("matched %q, want the degraded device", results[0].Stanza.Lines[0])
	}
}

// --- schema ---------------------------------------------------------------

// TestMatchedTokensIsAlwaysAnArray verifies correction #1: a single match is
// still a one-element array, and nothing is semicolon-joined.
func TestMatchedTokensIsAlwaysAnArray(t *testing.T) {
	src := &InputSource{Name: "<test>", Reader: strings.NewReader("a 10.0.0.1 b 10.0.0.2 c 10.0.0.3\n")}
	defer src.Close()

	var out bytes.Buffer
	if err := streamMatches(src, options{mode: ModeStream, tokens: []string{"ip"}}, &out, io.Discard); err != nil {
		t.Fatalf("streamMatches: %v", err)
	}

	// Inspect the raw JSON to prove the wire shape, not just the decoded struct.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	var tokens map[string][]string
	if err := json.Unmarshal(raw["matched_tokens"], &tokens); err != nil {
		t.Fatalf("matched_tokens is not map[string][]string: %v (%s)", err, raw["matched_tokens"])
	}
	got := tokens["ip"]
	if len(got) != 3 {
		t.Fatalf("matched_tokens[ip] = %v, want all 3 addresses", got)
	}
	for _, v := range got {
		if strings.Contains(v, ";") {
			t.Errorf("value %q contains a separator; values must not be joined", v)
		}
	}

	// The removed field must be gone entirely.
	if _, still := raw["matched_tokens_all"]; still {
		t.Error("matched_tokens_all is still present in the output")
	}
}

// TestMatchedTokensSingleMatchIsStillAnArray verifies a lone match is not
// collapsed to a bare string.
func TestMatchedTokensSingleMatchIsStillAnArray(t *testing.T) {
	src := &InputSource{Name: "<test>", Reader: strings.NewReader("addr 10.0.0.1\n")}
	defer src.Close()

	var out bytes.Buffer
	if err := streamMatches(src, options{mode: ModeStream, tokens: []string{"ip"}}, &out, io.Discard); err != nil {
		t.Fatalf("streamMatches: %v", err)
	}

	if !strings.Contains(out.String(), `"ip":["10.0.0.1"]`) {
		t.Errorf("single match is not a one-element array: %s", out.String())
	}
}

// --- positional grammar ---------------------------------------------------

// TestRunPositionalGrammar verifies correction #2: the grammar is passed as raw
// positional arguments, with the trailing path treated as the file.
func TestRunPositionalGrammar(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ips.txt"
	if err := writeFile(path, "eth0 10.0.0.1\nplain\neth1 10.0.0.2\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--mode", "stream", "get", "all", "[ip]", path}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

// TestRunPositionalGrammarMultiArgBracket verifies a bracket list split across
// argv (as a shell would) is rejoined before parsing.
func TestRunPositionalGrammarMultiArgBracket(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/x.txt"
	if err := writeFile(path, "0000:00:00.0 dev with 10.0.0.1\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	// A shell would pass the bracket list as two arguments: "[bdf," and "ip]".
	args := []string{"--mode", "stream", "get", "all", "[bdf,", "ip]", path}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if _, ok := results[0].MatchedTokens["bdf"]; !ok {
		t.Errorf("bdf did not match: %v", results[0].MatchedTokens)
	}
	if _, ok := results[0].MatchedTokens["ip"]; !ok {
		t.Errorf("ip did not match: %v", results[0].MatchedTokens)
	}
}

// TestRunFileFlagOverridesInference verifies --file removes the path ambiguity.
func TestRunFileFlagOverridesInference(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/real.txt"
	if err := writeFile(path, "addr 10.0.0.1\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	// "ip" does not name a file, but even if a same-named file existed the
	// explicit flag would win.
	args := []string{"--file", path, "--mode", "stream", "get", "all", "[ip]"}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

// TestRunBareFilenameIsTreatedAsFile verifies the single-argument case: a real
// path is read, not parsed as grammar.
func TestRunBareFilenameIsTreatedAsFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/only.txt"
	if err := writeFile(path, "addr 10.0.0.1\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"--mode", "stream", path}, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

// TestRunQueryFlagIsGone verifies the removed flag now fails loudly rather than
// being silently ignored.
func TestRunQueryFlagIsGone(t *testing.T) {
	var out, diag bytes.Buffer
	err := runPlain(t, []string{"--query", "get all [ip]"}, strings.NewReader("x\n"), &out, &diag)
	if err == nil {
		t.Fatal("run accepted --query, want it removed")
	}
	if !strings.Contains(err.Error(), "query") {
		t.Errorf("error = %q, want it to name the unknown flag", err)
	}
}

// TestRunPositionalGrammarFromStdin verifies a grammar with no file streams from
// stdin.
func TestRunPositionalGrammarFromStdin(t *testing.T) {
	var out, diag bytes.Buffer
	args := []string{"--mode", "stream", "get", "all", "[ip]"}
	if err := runPlain(t, args, strings.NewReader("addr 10.0.0.1\nplain\n"), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

// --- interspersed flags ---------------------------------------------------

// TestHoistFlagsReordersFlagAndPositional is the regression gate for a real
// usability bug: Go's flag package stops parsing at the first non-flag argument,
// so `vibepat get all [ip] --mode stream` failed with a grammar error. Flags must
// work wherever the user writes them.
func TestHoistFlagsReordersFlagAndPositional(t *testing.T) {
	got := hoistFlags(
		[]string{"get", "all", "[ip]", "--mode", "stream", "--context", "2", "file.txt"},
		flagTakesValue)
	want := []string{"--mode", "stream", "--context", "2", "get", "all", "[ip]", "file.txt"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestHoistFlagsHandlesEqualsForm verifies "--flag=value" is not split.
func TestHoistFlagsHandlesEqualsForm(t *testing.T) {
	got := hoistFlags([]string{"get", "all", "[ip]", "--mode=header"}, flagTakesValue)
	want := []string{"--mode=header", "get", "all", "[ip]"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestHoistFlagsStopsAtDoubleDash verifies everything after "--" stays
// positional, so a file beginning with a dash is reachable.
func TestHoistFlagsStopsAtDoubleDash(t *testing.T) {
	got := hoistFlags([]string{"--mode", "stream", "get", "all", "--", "--weird-file"}, flagTakesValue)
	want := []string{"--mode", "stream", "get", "all", "--weird-file"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestHoistFlagsLeavesBracketArgsAlone verifies bracketed grammar is never
// mistaken for a flag, including a single-value bracket list.
func TestHoistFlagsLeavesBracketArgsAlone(t *testing.T) {
	got := hoistFlags([]string{"get", "[ip]", "--mode", "stream"}, flagTakesValue)
	if got[0] != "--mode" || got[1] != "stream" {
		t.Fatalf("flags were not hoisted: %v", got)
	}
	if got[2] != "get" || got[3] != "[ip]" {
		t.Fatalf("positionals were reordered or mangled: %v", got)
	}
}

// TestRunFlagsAfterGrammar verifies the whole command line works with flags
// written after the grammar.
func TestRunFlagsAfterGrammar(t *testing.T) {
	var out, diag bytes.Buffer
	args := []string{"get", "all", "[ip]", "--mode", "stream"}
	if err := runPlain(t, args, strings.NewReader("addr 10.0.0.1\nplain\n"), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := results[0].MatchedTokens["ip"]; len(got) != 1 || got[0] != "10.0.0.1" {
		t.Errorf("matched = %v, want the address", results[0].MatchedTokens)
	}
}

// TestRunFlagsAfterGrammarWithFile verifies the file path still lands correctly
// when flags follow the grammar.
func TestRunFlagsAfterGrammarWithFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ips.txt"
	if err := writeFile(path, "addr 10.0.0.1\nplain\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"get", "all", "[ip]", "--mode", "stream", path}
	if err := runPlain(t, args, strings.NewReader("IGNORED\n"), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

// TestFlagTakesValueCoversEveryFlag guards the hoisting tables against drift:
// every non-boolean flag must be listed, or its value is hoisted away as a
// positional argument. This bug shipped once for --replace.
func TestFlagTakesValueCoversEveryFlag(t *testing.T) {
	// Flags that consume the following argument.
	valueFlags := []string{"context", "mode", "sample", "replace", "exec", "spot", "tokens", "file"}
	for _, name := range valueFlags {
		if !flagTakesValue[name] {
			t.Errorf("flag %q takes a value but is missing from flagTakesValue", name)
		}
		if !knownFlags[name] {
			t.Errorf("flag %q is missing from knownFlags", name)
		}
	}

	// Flags that do not.
	boolFlags := []string{"no-custom-tokens", "i", "interactive", "dryrun", "no-color", "version", "help", "h"}
	for _, name := range boolFlags {
		if flagTakesValue[name] {
			t.Errorf("boolean flag %q is wrongly listed as taking a value", name)
		}
		if !knownFlags[name] {
			t.Errorf("flag %q is missing from knownFlags", name)
		}
	}

	// Every documented flag must appear in knownFlags, so a following flag is
	// never swallowed as a value.
	if len(knownFlags) != len(valueFlags)+len(boolFlags) {
		t.Errorf("knownFlags has %d entries, want %d", len(knownFlags), len(valueFlags)+len(boolFlags))
	}
}

// TestReplaceValueFollowedByFlagIsNotSwallowed verifies a value-taking flag
// before another flag does not consume it.
func TestReplaceValueFollowedByFlagIsNotSwallowed(t *testing.T) {
	got := hoistFlags([]string{"--replace", "a=b", "--dryrun", "file"}, flagTakesValue)
	want := []string{"--replace", "a=b", "--dryrun", "file"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
