package main

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- Mutation tracking ----------------------------------------------------

// TestMockReplaceStanzaFindsAndReplaces verifies the Phase 3 demonstrator
// replacement and that mutations carry original-input line numbers.
func TestMockReplaceStanzaFindsAndReplaces(t *testing.T) {
	stanza := &Stanza{
		Lines: []string{
			"keep this",
			"value = MOCK_REPLACE",
			"also keep",
			"two MOCK_REPLACE here MOCK_REPLACE",
		},
		RawText:      strings.Join([]string{"keep this", "value = MOCK_REPLACE"}, "\n"),
		BoundaryType: BoundaryStream,
	}

	got := MockReplaceStanza(stanza, 10, []string{"ctx"})

	if len(got) != 2 {
		t.Fatalf("got %d mutations, want 2", len(got))
	}
	if got[0].LineNumber != 11 {
		t.Errorf("first mutation line = %d, want 11", got[0].LineNumber)
	}
	if got[0].OriginalText != "value = MOCK_REPLACE" {
		t.Errorf("original = %q", got[0].OriginalText)
	}
	if got[0].ModifiedText != "value = MOCK_SUCCESS" {
		t.Errorf("modified = %q, want MOCK_SUCCESS replacement", got[0].ModifiedText)
	}
	if got[1].LineNumber != 13 {
		t.Errorf("second mutation line = %d, want 13", got[1].LineNumber)
	}
	if got[1].ModifiedText != "two MOCK_SUCCESS here MOCK_SUCCESS" {
		t.Errorf("modified = %q, want all occurrences replaced", got[1].ModifiedText)
	}
}

// TestMockReplaceStanzaNoMatch verifies a clean stanza yields no mutations.
func TestMockReplaceStanzaNoMatch(t *testing.T) {
	stanza := &Stanza{Lines: []string{"nothing", "to do"}, BoundaryType: BoundaryStream}
	if got := MockReplaceStanza(stanza, 1, nil); len(got) != 0 {
		t.Fatalf("got %d mutations, want 0", len(got))
	}
}

// TestMockReplaceStanzaNilIsSafe verifies a nil stanza does not panic.
func TestMockReplaceStanzaNilIsSafe(t *testing.T) {
	if got := MockReplaceStanza(nil, 1, nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// --- Change plan validation ----------------------------------------------

// TestChangePlanValidateRejectsOutOfRange verifies a mutation past EOF is
// caught before anything is written.
func TestChangePlanValidateRejectsOutOfRange(t *testing.T) {
	plan := &ChangePlan{
		OriginalLines: []string{"a", "b"},
		Mutations:     []Mutation{{LineNumber: 5, OriginalText: "x", ModifiedText: "y"}},
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted an out-of-range line number")
	}
}

// TestChangePlanValidateRejectsOverlap verifies two mutations on one line are
// refused, since applying both would silently drop one.
func TestChangePlanValidateRejectsOverlap(t *testing.T) {
	plan := &ChangePlan{
		OriginalLines: []string{"a", "b"},
		Mutations: []Mutation{
			{LineNumber: 2, OriginalText: "b", ModifiedText: "x"},
			{LineNumber: 2, OriginalText: "b", ModifiedText: "y"},
		},
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted overlapping mutations")
	}
}

// TestChangePlanValidateRejectsStaleOriginal verifies the guard against
// applying a plan built against a different revision of the file.
func TestChangePlanValidateRejectsStaleOriginal(t *testing.T) {
	plan := &ChangePlan{
		OriginalLines: []string{"a", "b"},
		Mutations:     []Mutation{{LineNumber: 1, OriginalText: "NOT-A", ModifiedText: "x"}},
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted a mutation whose OriginalText does not match")
	}
}

// TestChangePlanApplyDoesNotMutateOriginal verifies Apply is pure, so a plan can
// be rendered for a dry run and then applied unchanged.
func TestChangePlanApplyDoesNotMutateOriginal(t *testing.T) {
	original := []string{"a", "MOCK_REPLACE", "c"}
	plan, err := NewChangePlan("", original, []Mutation{
		{LineNumber: 2, OriginalText: "MOCK_REPLACE", ModifiedText: "done"},
	})
	if err != nil {
		t.Fatalf("NewChangePlan: %v", err)
	}

	got := plan.Apply()
	if got[1] != "done" {
		t.Errorf("Apply result = %v, want [a done c]", got)
	}
	if original[1] != "MOCK_REPLACE" {
		t.Errorf("Apply mutated the input slice: %v", original)
	}
	if plan.OriginalLines[1] != "MOCK_REPLACE" {
		t.Errorf("Apply mutated plan.OriginalLines: %v", plan.OriginalLines)
	}

	// Idempotent: applying again yields the same answer.
	if again := plan.Apply(); again[1] != "done" {
		t.Errorf("second Apply = %v, want the same result", again)
	}
}

// --- Literal replacement --------------------------------------------------

// TestReplaceAllLiteral verifies multi-line, multi-occurrence replacement and
// original-input line numbering.
func TestReplaceAllLiteral(t *testing.T) {
	lines := []string{"10.0.1.1 alpha", "beta", "10.0.1.1 gamma 10.0.1.1"}
	got := ReplaceAllLiteral(lines, "10.0.1.1", "10.50.1.1", 0)

	if len(got) != 2 {
		t.Fatalf("got %d mutations, want 2", len(got))
	}
	if got[0].LineNumber != 1 || got[1].LineNumber != 3 {
		t.Errorf("line numbers = %d, %d; want 1, 3", got[0].LineNumber, got[1].LineNumber)
	}
	if got[1].ModifiedText != "10.50.1.1 gamma 10.50.1.1" {
		t.Errorf("modified = %q", got[1].ModifiedText)
	}
}

// TestReplaceAllLiteralEmptyPatternIsSafe verifies an empty pattern cannot
// match every position and produce garbage.
func TestReplaceAllLiteralEmptyPatternIsSafe(t *testing.T) {
	if got := ReplaceAllLiteral([]string{"abc"}, "", "X", 0); got != nil {
		t.Fatalf("got %v, want nil for an empty pattern", got)
	}
}

// TestReplaceAllLiteralContext verifies context capture is bounded and ordered.
func TestReplaceAllLiteralContext(t *testing.T) {
	lines := []string{"one", "two", "three", "hit", "five"}
	got := ReplaceAllLiteral(lines, "hit", "HIT", 2)

	if len(got) != 1 {
		t.Fatalf("got %d mutations, want 1", len(got))
	}
	want := []string{"two", "three"}
	if len(got[0].ContextLines) != len(want) {
		t.Fatalf("context = %v, want %v", got[0].ContextLines, want)
	}
	for i := range want {
		if got[0].ContextLines[i] != want[i] {
			t.Errorf("context[%d] = %q, want %q", i, got[0].ContextLines[i], want[i])
		}
	}
}

// --- Spot sampling --------------------------------------------------------

// TestSampleRandomSize is the headline spot gate: 100 mutations, request 5, get
// exactly 5.
func TestSampleRandomSize(t *testing.T) {
	mutations := make([]Mutation, 100)
	for i := range mutations {
		mutations[i] = Mutation{
			LineNumber:   i + 1,
			OriginalText: "old",
			ModifiedText: "new",
		}
	}

	got := SampleRandom(mutations, 5, rand.New(rand.NewSource(1)))
	if len(got) != 5 {
		t.Fatalf("got %d sampled mutations, want exactly 5", len(got))
	}
}

// TestSampleRandomIsRandomized verifies sampling actually varies with the seed
// rather than returning a fixed prefix.
func TestSampleRandomIsRandomized(t *testing.T) {
	mutations := make([]Mutation, 100)
	for i := range mutations {
		mutations[i] = Mutation{LineNumber: i + 1}
	}

	first := SampleRandom(mutations, 5, rand.New(rand.NewSource(1)))
	second := SampleRandom(mutations, 5, rand.New(rand.NewSource(2)))

	if sameLineNumbers(first, second) {
		t.Errorf("two different seeds produced the same sample: %v", first)
	}

	// It must not be a plain prefix of the input.
	prefix := true
	for i := range first {
		if first[i].LineNumber != i+1 {
			prefix = false
			break
		}
	}
	if prefix {
		t.Error("sample is just the first N mutations in order; not randomized")
	}
}

// TestSampleRandomSameSeedIsDeterministic verifies reproducibility, which is
// what makes spot mode testable at all.
func TestSampleRandomSameSeedIsDeterministic(t *testing.T) {
	mutations := make([]Mutation, 50)
	for i := range mutations {
		mutations[i] = Mutation{LineNumber: i + 1}
	}

	a := SampleRandom(mutations, 7, rand.New(rand.NewSource(99)))
	b := SampleRandom(mutations, 7, rand.New(rand.NewSource(99)))

	if !sameLineNumbers(a, b) {
		t.Errorf("same seed produced different samples: %v vs %v", a, b)
	}
}

// TestSampleRandomPreservesAscendingOrder verifies a spot-check reads like a
// coherent file excerpt rather than a shuffled list.
func TestSampleRandomPreservesAscendingOrder(t *testing.T) {
	mutations := make([]Mutation, 50)
	for i := range mutations {
		mutations[i] = Mutation{LineNumber: i + 1}
	}

	got := SampleRandom(mutations, 10, rand.New(rand.NewSource(7)))
	for i := 1; i < len(got); i++ {
		if got[i].LineNumber < got[i-1].LineNumber {
			t.Fatalf("sample is not in ascending line order: %v", got)
		}
	}
}

// TestSampleRandomEdgeCases verifies N larger than the set, N of zero, and an
// empty input all behave.
func TestSampleRandomEdgeCases(t *testing.T) {
	mutations := []Mutation{{LineNumber: 1}, {LineNumber: 2}}

	if got := SampleRandom(mutations, 10, nil); len(got) != 2 {
		t.Errorf("N > len: got %d, want 2", len(got))
	}
	if got := SampleRandom(mutations, 0, nil); got != nil {
		t.Errorf("N = 0: got %v, want nil", got)
	}
	if got := SampleRandom(nil, 5, nil); got != nil {
		t.Errorf("empty input: got %v, want nil", got)
	}
}

// TestSampleRandomDoesNotAliasInput verifies the returned mutations are copies,
// so mutating them cannot corrupt the plan.
func TestSampleRandomDoesNotAliasInput(t *testing.T) {
	mutations := []Mutation{{LineNumber: 1, ContextLines: []string{"x"}}}
	got := SampleRandom(mutations, 1, rand.New(rand.NewSource(1)))

	got[0].ContextLines[0] = "MUTATED"
	if mutations[0].ContextLines[0] != "x" {
		t.Fatal("SampleRandom aliased the input ContextLines")
	}
}

func sameLineNumbers(a, b []Mutation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].LineNumber != b[i].LineNumber {
			return false
		}
	}
	return true
}

// --- Backups --------------------------------------------------------------

// TestCreateBackupCopiesContentsExactly is the backup gate.
func TestCreateBackupCopiesContentsExactly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.conf")
	const content = "line one\nline two\n\tindented\n\n"
	if err := writeFile(path, content); err != nil {
		t.Fatalf("setup: %v", err)
	}

	backup, err := CreateBackup(path)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if backup != path+BackupSuffix {
		t.Errorf("backup path = %q, want %q", backup, path+BackupSuffix)
	}

	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != content {
		t.Errorf("backup contents = %q, want %q", got, content)
	}
}

// TestCreateBackupPreservesMode verifies a restrictive config does not become a
// world-readable backup.
func TestCreateBackupPreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.conf")
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	backup, err := CreateBackup(path)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("backup mode = %o, want 600", perm)
	}
}

// TestCreateBackupIsWrittenOnceAndPreserved verifies the chosen policy: a second
// run must not clobber the pristine original backup.
func TestCreateBackupIsWrittenOnceAndPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.conf")
	if err := writeFile(path, "ORIGINAL\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := CreateBackup(path); err != nil {
		t.Fatalf("first CreateBackup: %v", err)
	}

	// Simulate a modification after the first backup.
	if err := writeFile(path, "MUTATED\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := CreateBackup(path); err != nil {
		t.Fatalf("second CreateBackup: %v", err)
	}

	got, err := os.ReadFile(BackupPath(path))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != "ORIGINAL\n" {
		t.Errorf("backup = %q, want the pristine ORIGINAL preserved", got)
	}
}

// TestCreateBackupEmptyFile verifies a zero-length file backs up cleanly.
func TestCreateBackupEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.conf")
	if err := writeFile(path, ""); err != nil {
		t.Fatalf("setup: %v", err)
	}

	backup, err := CreateBackup(path)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("backup size = %d, want 0", info.Size())
	}
}

// TestCreateBackupMissingFile verifies a clear error rather than a stray backup.
func TestCreateBackupMissingFile(t *testing.T) {
	if _, err := CreateBackup(filepath.Join(t.TempDir(), "nope.conf")); err == nil {
		t.Fatal("CreateBackup on a missing file returned nil error")
	}
}

// TestCreateBackupStdinIsRejected verifies there is nothing to back up for a
// piped input.
func TestCreateBackupStdinIsRejected(t *testing.T) {
	if _, err := CreateBackup(""); err == nil {
		t.Fatal("CreateBackup(\"\") returned nil error, want a stdin error")
	}
}

// --- Atomic writes --------------------------------------------------------

// TestWriteAtomicReplacesContent verifies a successful write.
func TestWriteAtomicReplacesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.conf")
	if err := writeFile(path, "a\nb\nc\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := WriteAtomic(path, []string{"a", "B", "c"}); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "a\nB\nc\n" {
		t.Errorf("content = %q, want %q", got, "a\nB\nc\n")
	}
}

// TestWriteAtomicPreservesTrailingNewlineShape verifies a file with no trailing
// newline does not gain one, and vice versa.
func TestWriteAtomicPreservesTrailingNewlineShape(t *testing.T) {
	dir := t.TempDir()

	withNL := filepath.Join(dir, "with.conf")
	if err := writeFile(withNL, "a\nb\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := WriteAtomic(withNL, []string{"a", "B"}); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if got, _ := os.ReadFile(withNL); string(got) != "a\nB\n" {
		t.Errorf("with newline: %q, want %q", got, "a\nB\n")
	}

	withoutNL := filepath.Join(dir, "without.conf")
	if err := writeFile(withoutNL, "a\nb"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := WriteAtomic(withoutNL, []string{"a", "B"}); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if got, _ := os.ReadFile(withoutNL); string(got) != "a\nB" {
		t.Errorf("without newline: %q, want %q", got, "a\nB")
	}
}

// TestWriteAtomicLeavesNoTempFiles verifies the temporary file is renamed away,
// not left littering the directory.
func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.conf")
	if err := writeFile(path, "a\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := WriteAtomic(path, []string{"b"}); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "f.conf" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("directory contains %v, want only f.conf", names)
	}
}

// --- Execution modes ------------------------------------------------------

// newTestPlan builds a plan over n MOCK_REPLACE lines in a temp file and returns
// the plan and its path.
func newTestPlan(t *testing.T, n int) (*ChangePlan, string) {
	t.Helper()

	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("line ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(" = MOCK_REPLACE\n")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "target.conf")
	if err := writeFile(path, b.String()); err != nil {
		t.Fatalf("setup: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	mutations := ReplaceAllLiteral(lines, MockReplacement, MockReplacementWith, 0)
	plan, err := NewChangePlan(path, lines, mutations)
	if err != nil {
		t.Fatalf("NewChangePlan: %v", err)
	}
	return plan, path
}

// TestExecuteDefaultDeclinedWritesNothing verifies a 'n' answer leaves the file
// untouched and creates no backup.
func TestExecuteDefaultDeclinedWritesNothing(t *testing.T) {
	plan, path := newTestPlan(t, 3)
	before, _ := os.ReadFile(path)

	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{Mode: ModeDefault}, strings.NewReader("n\n"), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != StatusAborted {
		t.Errorf("status = %q, want %q", result.Status, StatusAborted)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Error("file was modified despite a declined prompt")
	}
	if _, err := os.Stat(BackupPath(path)); !os.IsNotExist(err) {
		t.Error("a backup was created despite a declined prompt")
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Errorf("output = %q, want an abort notice", out.String())
	}
}

// TestExecuteDefaultEmptyAnswerIsSafe verifies a bare Enter means no.
func TestExecuteDefaultEmptyAnswerIsSafe(t *testing.T) {
	plan, path := newTestPlan(t, 2)
	before, _ := os.ReadFile(path)

	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{Mode: ModeDefault}, strings.NewReader("\n"), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != StatusAborted {
		t.Errorf("status = %q, want %q", result.Status, StatusAborted)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("file changed on an empty answer")
	}
}

// TestExecuteDefaultConfirmedWritesAndBacksUp verifies the full happy path.
func TestExecuteDefaultConfirmedWritesAndBacksUp(t *testing.T) {
	plan, path := newTestPlan(t, 3)
	before, _ := os.ReadFile(path)

	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{Mode: ModeDefault}, strings.NewReader("y\n"), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != StatusApplied {
		t.Fatalf("status = %q, want %q", result.Status, StatusApplied)
	}
	if result.Applied != 3 {
		t.Errorf("applied = %d, want 3", result.Applied)
	}
	if result.BackupPath == "" {
		t.Error("no backup path reported")
	}

	after, _ := os.ReadFile(path)
	if strings.Contains(string(after), MockReplacement) {
		t.Errorf("file still contains the placeholder:\n%s", after)
	}
	if !strings.Contains(string(after), MockReplacementWith) {
		t.Errorf("file does not contain the replacement:\n%s", after)
	}

	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(before, backup) {
		t.Error("backup does not match the original contents")
	}
}

// TestExecuteYOLOSkipsPromptAndReportsJSON verifies that yolo writes with no
// input available and produces a machine-readable summary.
func TestExecuteYOLOSkipsPromptAndReportsJSON(t *testing.T) {
	plan, path := newTestPlan(t, 4)

	var out bytes.Buffer
	// An empty reader proves no prompt is consumed.
	result, err := Execute(plan, ExecOptions{Mode: ModeYOLO}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != StatusApplied {
		t.Fatalf("status = %q, want %q", result.Status, StatusApplied)
	}
	if result.Applied != 4 || result.Total != 4 {
		t.Errorf("applied/total = %d/%d, want 4/4", result.Applied, result.Total)
	}
	if result.BackupPath != BackupPath(path) {
		t.Errorf("backup = %q, want %q", result.BackupPath, BackupPath(path))
	}
	if !strings.Contains(string(mustRead(t, path)), MockReplacementWith) {
		t.Error("yolo mode did not write the replacement")
	}

	// In the real CLI the diff goes to stderr and the summary to stdout, so the
	// summary must be a standalone JSON document.
	var summary bytes.Buffer
	if err := WriteExecResult(&summary, result); err != nil {
		t.Fatalf("WriteExecResult: %v", err)
	}
	var decoded ExecResult
	if err := json.Unmarshal(summary.Bytes(), &decoded); err != nil {
		t.Fatalf("summary is not valid JSON: %v\n%s", err, summary.String())
	}
	if decoded.Applied != 4 {
		t.Errorf("decoded applied = %d, want 4", decoded.Applied)
	}
	if decoded.Status != StatusApplied {
		t.Errorf("decoded status = %q", decoded.Status)
	}
}

// TestExecuteYOLOEmitsNoANSI verifies the JSON summary is free of escape codes,
// since it is meant for CI consumption.
func TestExecuteYOLOEmitsNoANSI(t *testing.T) {
	plan, _ := newTestPlan(t, 2)

	var diff bytes.Buffer
	if _, err := Execute(plan, ExecOptions{Mode: ModeYOLO, Color: false}, strings.NewReader(""), &diff); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.Contains(diff.String(), "\x1b[") {
		t.Errorf("no-color output contained ANSI escapes: %q", diff.String())
	}
}

// TestExecuteColorEmitsANSI verifies color is applied when requested.
func TestExecuteColorEmitsANSI(t *testing.T) {
	plan, _ := newTestPlan(t, 2)

	var diff bytes.Buffer
	if _, err := Execute(plan, ExecOptions{Mode: ModeDefault, Color: true}, strings.NewReader("n\n"), &diff); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(diff.String(), "\x1b[") {
		t.Errorf("color output contained no ANSI escapes: %q", diff.String())
	}
}

// TestExecuteSpotShowsNAndCommitsAll is the central spot-mode contract: N diffs
// are displayed, but confirming writes every mutation in the plan.
func TestExecuteSpotShowsNAndCommitsAll(t *testing.T) {
	plan, path := newTestPlan(t, 20)

	var out bytes.Buffer
	opts := ExecOptions{
		Mode:  ModeSpot,
		SpotN: 5,
		RNG:   rand.New(rand.NewSource(3)),
	}
	result, err := Execute(plan, opts, strings.NewReader("y\n"), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Sampled != 5 {
		t.Errorf("sampled = %d, want 5", result.Sampled)
	}
	if result.Applied != 20 {
		t.Errorf("applied = %d, want all 20 committed", result.Applied)
	}

	content := string(mustRead(t, path))
	if strings.Contains(content, MockReplacement) {
		t.Error("spot mode left un-applied mutations behind")
	}
	if got := strings.Count(content, MockReplacementWith); got != 20 {
		t.Errorf("file contains %d replacements, want 20", got)
	}

	// Only 5 diffs should have been rendered.
	if got := strings.Count(out.String(), "@@ line"); got != 5 {
		t.Errorf("rendered %d diff hunks, want 5", got)
	}
	if !strings.Contains(out.String(), "Showing 5 of 20 changes") {
		t.Errorf("output does not state the sampling ratio:\n%s", out.String())
	}
}

// TestExecuteSpotDeclinedWritesNothing verifies spot mode is still gated by the
// prompt.
func TestExecuteSpotDeclinedWritesNothing(t *testing.T) {
	plan, path := newTestPlan(t, 10)
	before, _ := os.ReadFile(path)

	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{
		Mode:  ModeSpot,
		SpotN: 3,
		RNG:   rand.New(rand.NewSource(5)),
	}, strings.NewReader("n\n"), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != StatusAborted {
		t.Errorf("status = %q, want %q", result.Status, StatusAborted)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("file changed despite declining spot mode")
	}
}

// TestExecuteDryRunNeverWrites is the dry-run safety gate.
func TestExecuteDryRunNeverWrites(t *testing.T) {
	plan, path := newTestPlan(t, 5)
	before, _ := os.ReadFile(path)

	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{Mode: ModeYOLO, DryRun: true}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != StatusDryRun {
		t.Errorf("status = %q, want %q", result.Status, StatusDryRun)
	}
	if result.Applied != 0 {
		t.Errorf("applied = %d, want 0", result.Applied)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Error("dry run modified the file")
	}
	if _, err := os.Stat(BackupPath(path)); !os.IsNotExist(err) {
		t.Error("dry run created a backup")
	}
	if !strings.Contains(out.String(), "@@ line") {
		t.Error("dry run did not render a diff")
	}
}

// TestExecuteDryRunStillRendersSpotSample verifies dry-run composes with spot.
func TestExecuteDryRunStillRendersSpotSample(t *testing.T) {
	plan, _ := newTestPlan(t, 30)

	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{
		Mode:   ModeSpot,
		SpotN:  4,
		DryRun: true,
		RNG:    rand.New(rand.NewSource(11)),
	}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Sampled != 4 {
		t.Errorf("sampled = %d, want 4", result.Sampled)
	}
	if got := strings.Count(out.String(), "@@ line"); got != 4 {
		t.Errorf("rendered %d hunks, want 4", got)
	}
	if result.Applied != 0 {
		t.Errorf("applied = %d, want 0", result.Applied)
	}
}

// TestExecuteNoChangesIsNoOp verifies an empty plan reports cleanly and creates
// no backup.
func TestExecuteNoChangesIsNoOp(t *testing.T) {
	plan, path := newTestPlan(t, 1)
	plan.Mutations = nil

	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{Mode: ModeYOLO}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != StatusNoChanges {
		t.Errorf("status = %q, want %q", result.Status, StatusNoChanges)
	}
	if _, err := os.Stat(BackupPath(path)); !os.IsNotExist(err) {
		t.Error("a backup was created with nothing to change")
	}
}

// TestExecuteRejectsInvalidPlan verifies an unvalidatable plan is refused before
// the user is asked to approve it.
func TestExecuteRejectsInvalidPlan(t *testing.T) {
	plan, path := newTestPlan(t, 2)
	plan.Mutations[0].OriginalText = "STALE"

	before, _ := os.ReadFile(path)
	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{Mode: ModeYOLO}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("Execute accepted an invalid plan")
	}
	if result.Status != StatusError {
		t.Errorf("status = %q, want %q", result.Status, StatusError)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("invalid plan still modified the file")
	}
}

// TestExecuteStdinCannotBeWritten verifies the write path rejects a piped input
// instead of silently doing nothing.
func TestExecuteStdinCannotBeWritten(t *testing.T) {
	lines := []string{"a MOCK_REPLACE b"}
	plan, err := NewChangePlan("", lines, ReplaceAllLiteral(lines, MockReplacement, MockReplacementWith, 0))
	if err != nil {
		t.Fatalf("NewChangePlan: %v", err)
	}

	var out bytes.Buffer
	result, err := Execute(plan, ExecOptions{Mode: ModeYOLO}, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("Execute wrote a stdin-sourced plan, want an error")
	}
	if result.Status != StatusError {
		t.Errorf("status = %q, want %q", result.Status, StatusError)
	}
}

// TestNormalizeExecMode verifies mode parsing, including the force alias.
func TestNormalizeExecMode(t *testing.T) {
	cases := map[string]string{
		"":        ModeDefault,
		"default": ModeDefault,
		"DEFAULT": ModeDefault,
		"yolo":    ModeYOLO,
		"force":   ModeYOLO,
		"spot":    ModeSpot,
	}
	for in, want := range cases {
		got, err := NormalizeExecMode(in)
		if err != nil {
			t.Errorf("NormalizeExecMode(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeExecMode(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := NormalizeExecMode("bogus"); err == nil {
		t.Error("NormalizeExecMode accepted an unknown mode")
	}
}

// mustRead reads a file or fails the test.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
