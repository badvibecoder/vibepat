package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

// lines builds a synthetic input of n numbered lines.
func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line-")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

// processString is a test helper that runs the streaming read-only pipeline over
// an in-memory stream and decodes the NDJSON it produces.
func processString(t *testing.T, input string, opts options) []MatchResult {
	t.Helper()

	src := &InputSource{Name: "<test>", Reader: strings.NewReader(input)}
	defer src.Close()

	var out bytes.Buffer
	if err := streamMatches(src, opts, &out, io.Discard); err != nil {
		t.Fatalf("streamMatches: %v", err)
	}
	return decodeNDJSON(t, out.String())
}

// decodeNDJSON parses one JSON object per line into results. It fails the test on
// anything that is not a complete, self-contained object per line.
func decodeNDJSON(t *testing.T, text string) []MatchResult {
	t.Helper()

	var results []MatchResult
	for i, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m MatchResult
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d is not valid JSON (%v): %q", i+1, err, line)
		}
		results = append(results, m)
	}
	return results
}

// decodeSingleJSON decodes a whole document, used by the write paths which still
// emit one summary object.
func decodeSingleJSON(t *testing.T, text string) MatchResult {
	t.Helper()

	var m MatchResult
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, text)
	}
	return m
}

// runPlain invokes run with color disabled, the common case for tests.
func runPlain(t *testing.T, args []string, stdin io.Reader, out, diag *bytes.Buffer) error {
	t.Helper()
	return run(args, stdin, out, diag, false)
}

// TestProcessStreamModeYieldsOneStanzaPerLine verifies that stream mode reports
// every line as its own stanza.
func TestProcessStreamModeYieldsOneStanzaPerLine(t *testing.T) {
	results := processString(t, lines(4), options{mode: ModeStream})

	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	for i, want := range []int{1, 2, 3, 4} {
		if results[i].LineNumber != want {
			t.Errorf("results[%d].LineNumber = %d, want %d", i, results[i].LineNumber, want)
		}
		if results[i].StanzaIndex != i+1 {
			t.Errorf("results[%d].StanzaIndex = %d, want %d", i, results[i].StanzaIndex, i+1)
		}
		if results[i].PositionKind != PositionStanza {
			t.Errorf("results[%d].PositionKind = %q, want %q", i, results[i].PositionKind, PositionStanza)
		}
		if results[i].Stanza == nil {
			t.Fatalf("results[%d].Stanza is nil", i)
		}
		if got := results[i].Stanza.BoundaryType; got != BoundaryStream {
			t.Errorf("results[%d] BoundaryType = %q, want %q", i, got, BoundaryStream)
		}
	}
}

// TestProcessContextExcludesMatchedStanza is the behavioral gate on snapshot
// ordering: with --context 2 in stream mode, the line-3 stanza must carry lines
// 1 and 2 and must not include line 3 itself.
func TestProcessContextExcludesMatchedStanza(t *testing.T) {
	results := processString(t, lines(3), options{mode: ModeStream, context: 2})

	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	got := results[2].ContextLines
	want := []string{"line-1", "line-2"}
	if len(got) != len(want) {
		t.Fatalf("ContextLines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ContextLines = %v, want %v", got, want)
		}
	}
}

// TestProcessHeaderModeGroupsDevice ensures the pipeline reports one result per
// device block, not one per line.
func TestProcessHeaderModeGroupsDevice(t *testing.T) {
	const input = `00:00.0 Host bridge: Intel Corporation
	Flags: fast devsel
	Capabilities: [e0] Vendor Specific Information
00:02.0 VGA compatible controller: Intel Corporation
	Flags: fast devsel

`
	results := processString(t, input, options{mode: ModeHeader})

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 device stanzas", len(results))
	}
	if got := results[0].Stanza.Lines[0]; !strings.HasPrefix(got, "00:00.0") {
		t.Errorf("first stanza starts with %q, want the 00:00.0 header", got)
	}
	if got := len(results[0].Stanza.Lines); got != 3 {
		t.Errorf("first stanza has %d lines, want 3", got)
	}
	if got := results[1].LineNumber; got != 5 {
		t.Errorf("second stanza LineNumber = %d, want 5", got)
	}
}

// TestProcessEmptyInput verifies an empty stream is handled cleanly.
func TestProcessEmptyInput(t *testing.T) {
	results := processString(t, "", options{mode: ModeStream})
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
}

// TestRunFromStdinEmitsValidJSONArray verifies the end-to-end contract: piping
// into the binary yields exactly one parseable JSON array.
func TestRunFromStdinEmitsValidJSONArray(t *testing.T) {
	var out, diag bytes.Buffer
	args := []string{"--mode", "stream", "--context", "2"}
	if err := runPlain(t, args, strings.NewReader(lines(5)), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 5 {
		t.Fatalf("got %d results, want 5", len(results))
	}
	if got := results[2].ContextLines; len(got) != 2 || got[0] != "line-1" || got[1] != "line-2" {
		t.Fatalf("ContextLines = %v, want [line-1 line-2]", got)
	}
}

// TestRunReportsDetectedModeOnDiag verifies that auto-detection is announced on
// the diagnostic stream and never leaks into the JSON.
func TestRunReportsDetectedModeOnDiag(t *testing.T) {
	const input = `00:00.0 Host bridge: Intel Corporation
	Flags: fast devsel
00:02.0 VGA compatible controller: Intel Corporation
	Flags: fast devsel
`
	var out, diag bytes.Buffer
	if err := runPlain(t, nil, strings.NewReader(input), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(diag.String(), ModeHeader) {
		t.Errorf("diag = %q, want it to mention the detected mode %q", diag.String(), ModeHeader)
	}
	if strings.Contains(out.String(), "auto-detected") {
		t.Errorf("diagnostics leaked into JSON output: %s", out.String())
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

// TestRunEmptyInputEmitsEmptyArray verifies empty stdin yields [] and not null,
// so consumers can always range over the result.
func TestRunEmptyInputEmitsEmptyArray(t *testing.T) {
	var out, diag bytes.Buffer
	if err := runPlain(t, nil, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := out.String(); got != "" {
		t.Fatalf("empty input produced %q, want no output at all", got)
	}
}

// TestRunRejectsNegativeContext verifies flag validation.
func TestRunRejectsNegativeContext(t *testing.T) {
	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"--context", "-1"}, strings.NewReader("x\n"), &out, &diag); err == nil {
		t.Fatal("run accepted --context -1, want error")
	}
}

// TestRunRejectsUnknownMode verifies an invalid --mode is a hard error rather
// than a silent fallback to stream.
func TestRunRejectsUnknownMode(t *testing.T) {
	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"--mode", "bogus"}, strings.NewReader("x\n"), &out, &diag); err == nil {
		t.Fatal("run accepted --mode bogus, want error")
	}
}

// TestRunRejectsUnknownFlag verifies flag parse errors are surfaced.
func TestRunRejectsUnknownFlag(t *testing.T) {
	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"--nope"}, strings.NewReader("x\n"), &out, &diag); err == nil {
		t.Fatal("run accepted an unknown flag, want error")
	}
}

// TestRunReadsFileArgument verifies the positional path is honored.
func TestRunReadsFileArgument(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/input.log"
	if err := writeFile(path, lines(3)); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"--mode", "stream", path}, strings.NewReader("IGNORED\n"), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	results := decodeNDJSON(t, out.String())
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Stanza.Lines[0] != "line-1" {
		t.Fatalf("first stanza = %q, want line-1", results[0].Stanza.Lines[0])
	}
}

// TestRunVersion verifies --version short-circuits without touching input.
func TestRunVersion(t *testing.T) {
	var out, diag bytes.Buffer
	if err := runPlain(t, []string{"--version"}, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), version) {
		t.Fatalf("version output = %q, want it to contain %q", out.String(), version)
	}
}

// --- Replace pipeline, end to end ----------------------------------------

// TestRunReplaceDryRunDoesNotWrite is the CLI-level dry-run gate.
func TestRunReplaceDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/target.conf"
	const content = "addr = 10.0.1.1\nkeep = me\n"
	if err := writeFile(path, content); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--replace", "10.0.1.1=10.50.1.1", "--dryrun", "--exec", "yolo", path}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("dry run modified the file: %q", got)
	}
	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Error("dry run created a backup")
	}

	var result ExecResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if result.Status != StatusDryRun {
		t.Errorf("status = %q, want %q", result.Status, StatusDryRun)
	}
	if result.Total != 1 {
		t.Errorf("total = %d, want 1", result.Total)
	}
}

// TestRunReplaceYOLOWritesBackupAndSummary is the full CLI happy path.
func TestRunReplaceYOLOWritesBackupAndSummary(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/target.conf"
	if err := writeFile(path, "addr = 10.0.1.1\nkeep = me\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--replace", "10.0.1.1=10.50.1.1", "--exec", "yolo", "--no-color", path}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "addr = 10.50.1.1\nkeep = me\n" {
		t.Errorf("file = %q, want the replacement applied", got)
	}

	backup, err := os.ReadFile(path + BackupSuffix)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != "addr = 10.0.1.1\nkeep = me\n" {
		t.Errorf("backup = %q, want the original", backup)
	}

	var result ExecResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if result.Status != StatusApplied || result.Applied != 1 {
		t.Errorf("result = %+v, want applied=1", result)
	}
	if result.BackupPath != path+BackupSuffix {
		t.Errorf("backup path = %q, want %q", result.BackupPath, path+BackupSuffix)
	}
}

// TestRunReplaceDeclinedWritesNothing verifies the interactive prompt gating
// through the CLI, with the diff on the diagnostic stream.
func TestRunReplaceDeclinedWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/target.conf"
	const content = "addr = 10.0.1.1\n"
	if err := writeFile(path, content); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--replace", "10.0.1.1=10.50.1.1", "--no-color", path}
	if err := runPlain(t, args, strings.NewReader("n\n"), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got, _ := os.ReadFile(path); string(got) != content {
		t.Errorf("declined prompt modified the file: %q", got)
	}
	if !strings.Contains(diag.String(), "@@ line") {
		t.Errorf("diff was not shown on the diagnostic stream:\n%s", diag.String())
	}
	// stdout must remain a clean JSON document.
	var result ExecResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if result.Status != StatusAborted {
		t.Errorf("status = %q, want %q", result.Status, StatusAborted)
	}
}

// TestRunReplaceStdinIsRejected verifies a piped replace cannot silently no-op.
func TestRunReplaceStdinIsRejected(t *testing.T) {
	var out, diag bytes.Buffer
	args := []string{"--replace", "a=b", "--exec", "yolo"}
	err := runPlain(t, args, strings.NewReader("a\n"), &out, &diag)
	if err == nil {
		t.Fatal("run accepted a stdin-fed replace, want an error")
	}
}

// TestRunReplaceInvalidRule verifies a malformed rule is a usage error.
func TestRunReplaceInvalidRule(t *testing.T) {
	var out, diag bytes.Buffer
	for _, bad := range []string{"no-separator", "=emptypattern"} {
		err := runPlain(t, []string{"--replace", bad}, strings.NewReader("x\n"), &out, &diag)
		if err == nil {
			t.Errorf("run accepted malformed --replace %q, want an error", bad)
		}
	}
}

// TestRunReplaceAllowsEqualsInReplacement verifies the split uses the first '='
// so replacement text may itself contain '='.
func TestRunReplaceAllowsEqualsInReplacement(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/t.conf"
	if err := writeFile(path, "k = OLD\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--replace", "OLD=NEW=EXTRA", "--exec", "yolo", "--no-color", path}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got, _ := os.ReadFile(path); string(got) != "k = NEW=EXTRA\n" {
		t.Errorf("file = %q, want %q", got, "k = NEW=EXTRA\n")
	}
}

// TestRunReplaceDuplicateRulesOnSameLineDoesNotCorrupt verifies two rules
// matching one line compose into a single mutation rather than colliding.
func TestRunReplaceDuplicateRulesOnSameLineDoesNotCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/t.conf"
	if err := writeFile(path, "alpha and beta\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--replace", "alpha=ALPHA", "--replace", "beta=BETA", "--exec", "yolo", "--no-color", path}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got, _ := os.ReadFile(path); string(got) != "ALPHA and BETA\n" {
		t.Errorf("file = %q, want %q", got, "ALPHA and BETA\n")
	}
	var result ExecResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result.Applied != 1 {
		t.Errorf("applied = %d, want 1 (one line, changed once)", result.Applied)
	}
}

// TestRunReplaceSpotThroughCLI verifies spot mode end to end.
func TestRunReplaceSpotThroughCLI(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/t.conf"
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("host = 10.0.1.1\n")
	}
	if err := writeFile(path, b.String()); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{"--replace", "10.0.1.1=10.50.1.1", "--exec", "spot", "--spot", "5", "--no-color", path}
	if err := runPlain(t, args, strings.NewReader("y\n"), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := strings.Count(diag.String(), "@@ line"); got != 5 {
		t.Errorf("rendered %d diff hunks, want 5", got)
	}
	content, _ := os.ReadFile(path)
	if got := strings.Count(string(content), "10.50.1.1"); got != 20 {
		t.Errorf("file has %d replacements, want all 20 committed", got)
	}
}
