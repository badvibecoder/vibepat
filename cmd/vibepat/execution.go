package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"
)

// Execution statuses reported in the JSON summary.
const (
	// StatusApplied means the mutations were written to disk.
	StatusApplied = "applied"
	// StatusNoChanges means there was nothing to do.
	StatusNoChanges = "no_changes"
	// StatusAborted means the user declined the confirmation prompt.
	StatusAborted = "aborted"
	// StatusDryRun means the diff was computed but nothing was written.
	StatusDryRun = "dryrun"
	// StatusError means execution failed before or during the write.
	StatusError = "error"
)

// diffContext is how many unchanged lines surround each change in the rendered
// diff. One line of context is enough to orient the reader without burying the
// actual change.
const diffContext = 1

// ANSI color codes used by the diff renderer.
const (
	ansiReset = "\x1b[0m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiCyan  = "\x1b[36m"
	ansiBold  = "\x1b[1m"
)

// ExecOptions configures how a ChangePlan is presented and committed.
type ExecOptions struct {
	// Mode is ModeDefault, ModeYOLO, or ModeSpot.
	Mode string
	// SpotN is the number of sampled diffs shown in ModeSpot.
	SpotN int
	// DryRun computes the diff and summary but never writes.
	DryRun bool
	// Color enables ANSI styling in the rendered diff. Callers should derive
	// this from whether the destination is a terminal.
	Color bool
	// RNG is the randomness source for sampling. Nil means a fresh
	// time-seeded source; tests inject a deterministic one.
	RNG *rand.Rand
}

// Execution modes for the --exec flag.
const (
	// ModeDefault shows the full diff and prompts before writing.
	ModeDefault = "default"
	// ModeYOLO writes immediately without prompting and emits a JSON summary.
	ModeYOLO = "yolo"
	// ModeSpot shows a random sample of the diffs and prompts once for the
	// whole batch.
	ModeSpot = "spot"
)

// NormalizeExecMode validates and canonicalizes an execution mode.
func NormalizeExecMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeDefault:
		return ModeDefault, nil
	case ModeYOLO, "force":
		return ModeYOLO, nil
	case ModeSpot:
		return ModeSpot, nil
	default:
		return "", fmt.Errorf("unknown execution mode %q (want default, yolo, or spot)", mode)
	}
}

// ExecResult is the machine-readable outcome of an execution attempt. It is
// emitted as JSON in YOLO and dry-run modes so CI pipelines can consume it.
type ExecResult struct {
	// Status is one of the Status* constants.
	Status string `json:"status"`
	// SourcePath is the file that was (or would be) modified.
	SourcePath string `json:"source_path"`
	// BackupPath is the backup that was created, or "" if none was needed.
	BackupPath string `json:"backup_path"`
	// Applied is the number of mutations written.
	Applied int `json:"applied"`
	// Total is the number of mutations in the plan.
	Total int `json:"total"`
	// Sampled is the number of diffs actually shown to the user in spot mode.
	Sampled int `json:"sampled"`
	// Mutations lists the changes that were applied or sampled.
	Mutations []Mutation `json:"mutations"`
	// Error carries a failure reason when Status is StatusError.
	Error string `json:"error"`
}

// Execute presents plan according to opts and, if confirmed, commits it.
//
// The confirmation contract for ModeSpot is deliberate: N randomly chosen diffs
// are shown so the operator can judge the change class, but confirming commits
// the *entire* plan. Showing only a sample and then writing only that sample
// would silently discard the rest of the work, which is a far worse failure
// than showing an incomplete preview.
//
// Nothing is ever written unless the plan validates, the user confirms (or YOLO
// was requested), and the run is not a dry run.
func Execute(plan *ChangePlan, opts ExecOptions, in io.Reader, out io.Writer) (*ExecResult, error) {
	result := &ExecResult{
		SourcePath: plan.SourcePath,
		Total:      len(plan.Mutations),
		Mutations:  []Mutation{},
	}

	if len(plan.Mutations) == 0 {
		result.Status = StatusNoChanges
		return result, nil
	}

	// Validate before showing anything: a diff the user cannot safely approve
	// must not be presented as approvable.
	if err := plan.Validate(); err != nil {
		result.Status = StatusError
		result.Error = err.Error()
		return result, err
	}

	shown := plan.Mutations
	if opts.Mode == ModeSpot {
		shown = SampleRandom(plan.Mutations, opts.SpotN, opts.RNG)
	}
	result.Sampled = len(shown)

	renderDiff(out, plan, shown, opts.Color)

	if opts.Mode == ModeSpot {
		fmt.Fprintf(out, "\nShowing %d of %d changes (spot check).\n", len(shown), len(plan.Mutations))
	}

	if opts.DryRun {
		result.Status = StatusDryRun
		result.Mutations = shown
		return result, nil
	}

	if opts.Mode != ModeYOLO {
		ok, err := confirm(in, out, len(plan.Mutations))
		if err != nil {
			result.Status = StatusError
			result.Error = err.Error()
			return result, err
		}
		if !ok {
			result.Status = StatusAborted
			result.Mutations = shown
			fmt.Fprintln(out, "Aborted. No changes were written.")
			return result, nil
		}
	}

	backup, err := commit(plan)
	if err != nil {
		result.Status = StatusError
		result.Error = err.Error()
		return result, err
	}

	result.Status = StatusApplied
	result.BackupPath = backup
	result.Applied = len(plan.Mutations)
	result.Mutations = plan.Mutations
	return result, nil
}

// commit backs up the source and atomically writes the mutated content.
func commit(plan *ChangePlan) (string, error) {
	if plan.SourcePath == "" {
		return "", errNotAFile
	}

	backup, err := CreateBackup(plan.SourcePath)
	if err != nil {
		return "", err
	}
	if err := WriteAtomic(plan.SourcePath, plan.Apply()); err != nil {
		// The backup is intact, so the original is recoverable; say so rather
		// than leaving the operator guessing.
		return backup, fmt.Errorf("%w (original preserved at %s)", err, backup)
	}
	return backup, nil
}

// confirm prompts on out and reads a single answer from in. Any answer other
// than y/Y/yes is treated as no, so a bare Enter is safe.
func confirm(in io.Reader, out io.Writer, count int) (bool, error) {
	fmt.Fprintf(out, "\nCommit %s? [y/N] ", pluralize(count, "change"))

	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read confirmation: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// pluralize renders "1 change" or "N changes".
func pluralize(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// renderDiff writes a unified-style diff for the given mutations.
func renderDiff(out io.Writer, plan *ChangePlan, mutations []Mutation, color bool) {
	if len(mutations) == 0 {
		return
	}

	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + ansiReset
	}

	for i, m := range mutations {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out, paint(ansiCyan,
			fmt.Sprintf("@@ line %d @@", m.LineNumber)))

		// One line of leading context, with its real line number.
		if len(m.ContextLines) > 0 {
			start := m.LineNumber - len(m.ContextLines)
			for j, ctx := range m.ContextLines {
				fmt.Fprintln(out, fmt.Sprintf(" %5d  %s", start+j, ctx))
			}
		}

		fmt.Fprintln(out, paint(ansiRed, fmt.Sprintf("-%5d  %s", m.LineNumber, m.OriginalText)))
		fmt.Fprintln(out, paint(ansiGreen, fmt.Sprintf("+%5d  %s", m.LineNumber, m.ModifiedText)))
	}
}

// WriteExecResult serializes an execution summary as indented JSON.
func WriteExecResult(out io.Writer, result *ExecResult) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("encode execution result: %w", err)
	}
	return nil
}
