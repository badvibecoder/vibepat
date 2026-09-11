// Command vibepat slices, filters, and rewrites log, config, and hardware
// topology text streams.
//
// Phase 1 established the plumbing: input resolution, the context ring-buffer,
// and the MatchResult JSON contract. Phase 2 added visual stanza chunking.
// Phase 3 adds the mutation tracker, backups, and the default/yolo/spot
// execution modes.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"vibepat/src/registry"
)

// version is the semantic version reported by --version.
//
// It is a variable rather than a constant so that release builds can stamp it
// with -ldflags "-X main.version=...". A constant cannot be overridden by the
// linker, which would make such a flag silently ineffective.
var version = "1.1.0"

// exitError distinguishes a usage/runtime failure from a successful run.
const exitError = 1

// options holds the parsed command line.
type options struct {
	// context is the number of preceding lines to embed in each match.
	context int
	// path is the file to read, or "" to read standard input.
	path string
	// mode selects the visual chunking heuristic.
	mode string
	// sampleSize is how many leading lines ModeAuto inspects.
	sampleSize int
	// replaces holds literal OLD=NEW replacement rules.
	replaces []string
	// execMode is the Phase 3 execution mode: default, yolo, or spot.
	execMode string
	// spotN is how many sampled diffs spot mode shows.
	spotN int
	// tokens names the semantic tokens to match, or nil for all registered
	// tokens.
	tokens []string
	// tokenList is the raw --tokens flag value.
	tokenList string
	// noCustomTokens disables loading ~/.vibepat/custom.yaml.
	noCustomTokens bool
	// filterTokens reports only stanzas that matched at least one requested
	// token. It is enabled whenever --tokens is supplied.
	filterTokens bool
	// positional holds the non-flag arguments, which carry the grammar command
	// and optionally a file path.
	positional []string
	// file is the explicit --file path, which overrides inference.
	file string
	// parsed holds the compiled query when one was supplied.
	parsed *Query
	// interactive requests the REPL.
	interactive bool
	// dryRun computes the diff but never writes.
	dryRun bool
	// noColor disables ANSI styling in the rendered diff.
	noColor bool
	// showVersion is set by --version and short-circuits normal execution.
	showVersion bool
	// showHelp is set by --help and prints the help index.
	showHelp bool
}

// replacePair is a parsed OLD=NEW literal replacement.
type replacePair struct {
	old string
	new string
}

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	color := isTerminal(os.Stdout)
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, color); err != nil {
		fmt.Fprintf(os.Stderr, "vibepat: %v\n", err)
		os.Exit(exitError)
	}
}

// run is the testable entry point. It parses args, resolves input, chunks it
// into stanzas, and writes the JSON array of matches to out. Diagnostics that
// must not pollute the JSON stream (such as the auto-detected mode) go to
// diag. stdoutIsTTY seeds the color default for the interactive diff.
func run(args []string, stdin io.Reader, out, diag io.Writer, stdoutIsTTY bool) error {
	helpEnv := helpEnvFor(out, stdoutIsTTY)

	// "vibepat help [topic]" is a first-class command, routed before flag parsing
	// because "help" is a positional argument and the rest of the line is a topic
	// rather than grammar.
	if handled, err := runHelp(args, helpEnv); handled {
		return err
	}

	opts, err := parseFlags(args)
	if err != nil {
		// The flag package reports -h and --help as ErrHelp. Both print the
		// index of topics rather than the full manual, so that --help stays a
		// quick orientation and the manual is one deliberate step away.
		if errors.Is(err, flag.ErrHelp) {
			return WriteHelpIndex(helpEnv)
		}
		return err
	}
	if opts.showHelp {
		return WriteHelpIndex(helpEnv)
	}
	if opts.showVersion {
		_, err := fmt.Fprintf(out, "vibepat %s\n", version)
		return err
	}

	// Custom tokens are additive and optional. A broken custom file is a hard
	// error: silently ignoring it would make the tool stop matching something
	// the operator explicitly asked for.
	if !opts.noCustomTokens {
		loaded, err := registry.LoadCustomDefault()
		if err != nil {
			return err
		}
		if loaded > 0 {
			fmt.Fprintf(diag, "vibepat: loaded %s of custom tokens\n", pluralize(loaded, "token"))
		}
	}

	// A grammar command is written as bare positional arguments, so which
	// argument is the file has to be decided before the input is opened. The
	// explicit --file flag wins over any inference.
	grammar, path, err := splitPositionals(opts.positional, opts.file)
	if err != nil {
		return err
	}
	opts.path = path

	if len(grammar) > 0 {
		q, err := Parse(strings.Join(grammar, " "))
		if err != nil {
			return err
		}
		if err := q.ValidateExecutable(); err != nil {
			return err
		}
		opts.parsed = q
	}

	// Resolve the requested token set once, so an unknown name is reported here
	// rather than being silently dropped per stanza.
	if err := resolveTokens(&opts); err != nil {
		return err
	}

	// stdout is reserved for the machine-readable result, so the human-facing
	// diff always goes to the diagnostic stream whenever the run can write.
	// Otherwise there is no diff and stdout carries the match stream.
	//
	// This covers both write paths: a --replace rule and a replace query.
	diffOut := out
	if len(opts.replaces) > 0 || (opts.parsed != nil && opts.parsed.IsWrite()) {
		diffOut = diag
	}

	src, err := OpenInput(opts.path)
	if err != nil {
		return err
	}
	defer src.Close()

	// OpenInput with an empty path falls back to os.Stdin. When run is invoked
	// from a test with an injected reader, re-point the source at it so the
	// testable path and the real binary path agree.
	if opts.path == "" {
		src.Reader = stdin
	}

	// Read-only runs stream NDJSON; write runs must hold the whole input because
	// a mutation plan is replayed against the original file.
	switch {
	case opts.interactive:
		return processInteractive(src, opts, out, diag, stdin, stdoutIsTTY)
	case opts.parsed != nil && opts.parsed.IsWrite():
		return processQueryWrite(src, opts.parsed, opts, out, diffOut, stdin, stdoutIsTTY)
	case opts.parsed != nil:
		return streamQuery(src, opts.parsed, opts, out, diag)
	case len(opts.replaces) > 0:
		return processReplace(src, opts, out, diffOut, stdin, stdoutIsTTY)
	default:
		return streamMatches(src, opts, out, diag)
	}
}

// parseFlags parses the vibepat command line. It returns a descriptive error
// for unknown flags and validates the positional argument count.
func parseFlags(args []string) (options, error) {
	var opts options
	var replaces stringList

	fs := flag.NewFlagSet("vibepat", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // run owns error reporting.
	fs.IntVar(&opts.context, "context", 0, "number of preceding lines to embed in each match")
	fs.StringVar(&opts.mode, "mode", ModeAuto, "chunking heuristic: auto, stream, header, or indent")
	fs.IntVar(&opts.sampleSize, "sample", DefaultSampleSize, "leading lines inspected when --mode is auto")
	fs.Var(&replaces, "replace", "literal OLD=NEW replacement; repeatable")
	fs.StringVar(&opts.execMode, "exec", ModeDefault, "execution mode: default, yolo, or spot")
	fs.IntVar(&opts.spotN, "spot", 5, "number of sampled diffs shown in spot mode")
	fs.StringVar(&opts.tokenList, "tokens", "", "comma-separated semantic tokens to match (default: all registered)")
	fs.BoolVar(&opts.noCustomTokens, "no-custom-tokens", false, "do not load ~/.vibepat/custom.yaml")
	fs.StringVar(&opts.file, "file", "", "input file; overrides path detection in the positional arguments")
	fs.BoolVar(&opts.interactive, "i", false, "start the interactive REPL")
	fs.BoolVar(&opts.interactive, "interactive", false, "start the interactive REPL (long form)")
	fs.BoolVar(&opts.dryRun, "dryrun", false, "compute the diff and summary but never write")
	fs.BoolVar(&opts.noColor, "no-color", false, "disable ANSI color in the rendered diff")
	fs.BoolVar(&opts.showVersion, "version", false, "print version and exit")
	fs.BoolVar(&opts.showHelp, "help", false, "print the help index and exit")

	// Flags may be written after the grammar, which is the natural order for a
	// command like `vibepat get all [bdf] --mode header file`. Go's flag package
	// stops at the first non-flag argument, so flags are hoisted out first.
	if err := fs.Parse(hoistFlags(args, flagTakesValue)); err != nil {
		return options{}, err
	}
	if opts.context < 0 {
		return options{}, fmt.Errorf("--context must be >= 0, got %d", opts.context)
	}
	if _, err := normalizeMode(opts.mode); err != nil {
		return options{}, err
	}
	if _, err := NormalizeExecMode(opts.execMode); err != nil {
		return options{}, err
	}
	if opts.sampleSize < 0 {
		return options{}, fmt.Errorf("--sample must be >= 0, got %d", opts.sampleSize)
	}
	if opts.spotN < 0 {
		return options{}, fmt.Errorf("--spot must be >= 0, got %d", opts.spotN)
	}
	for _, r := range replaces {
		if _, _, err := splitReplace(r); err != nil {
			return options{}, err
		}
	}
	opts.replaces = replaces
	opts.tokens = registry.SplitTokenList(opts.tokenList)
	opts.positional = fs.Args()

	// Positional arguments carry the grammar command and optionally a file path;
	// see splitPositionals. They are validated there, once the filesystem can be
	// consulted.
	return opts, nil
}

// knownFlags is every flag the command accepts. It lets hoisting refuse to treat
// a following flag as a missing value.
var knownFlags = map[string]bool{
	"context": true, "mode": true, "sample": true, "replace": true,
	"exec": true, "spot": true, "tokens": true, "no-custom-tokens": true,
	"file": true, "i": true, "interactive": true, "dryrun": true,
	"no-color": true, "version": true, "help": true, "h": true,
}

// flagTakesValue names the flags that consume the following argument, which is
// what makes hoisting them out of the positional run possible.
var flagTakesValue = map[string]bool{
	"context": true,
	"mode":    true,
	"sample":  true,
	"replace": true,
	"exec":    true,
	"spot":    true,
	"tokens":  true,
	"file":    true,
}

// hoistFlags moves flag arguments ahead of positional arguments so that flags may
// be written anywhere on the command line.
//
// It understands "--flag value", "--flag=value", and boolean "--flag". A bare
// "--" ends flag interpretation, and everything after it is positional, so a
// file whose name begins with a dash remains reachable.
func hoistFlags(args []string, takesValue map[string]bool) []string {
	var flags, positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !isFlagArg(arg) {
			positional = append(positional, arg)
			continue
		}

		flags = append(flags, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		if !takesValue[strings.TrimLeft(arg, "-")] {
			continue
		}

		// A value-taking flag consumes the next argument, but never a following
		// flag: `--replace --dryrun` means --replace has no value, not that its
		// value is the literal text "--dryrun". Passing the flag through instead
		// lets the flag package report the missing value, which is the same
		// diagnosis the user would get with the flags in the other order.
		if i+1 < len(args) {
			next := args[i+1]
			if !isFlagArg(next) || !knownFlags[strings.TrimLeft(strings.SplitN(next, "=", 2)[0], "-")] {
				flags = append(flags, next)
				i++
			}
		}
	}

	return append(flags, positional...)
}

// helpEnvFor builds the help writer environment. Help is paged only when stdout
// is a real terminal; a pipe gets clean plaintext.
func helpEnvFor(out io.Writer, stdoutIsTTY bool) helpEnv {
	return helpEnv{Out: out, IsTerminal: stdoutIsTTY}
}

// isFlagArg reports whether arg is a flag rather than a positional argument.
//
// A lone "-" is positional, the usual convention for stdin. Beyond that, the
// argument must name a flag this program actually defines: a negative number in
// a grammar clause ("with context -1") also begins with a dash, and treating it
// as an unknown flag would report the wrong error. An unrecognized flag is still
// reported, because the flag package sees the unhoisted argument and rejects it.
func isFlagArg(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' {
		return false
	}
	name := strings.SplitN(strings.TrimLeft(arg, "-"), "=", 2)[0]
	return knownFlags[name]
}

// splitPositionals divides the non-flag arguments into a grammar command and an
// optional input file.
//
// The grammar is written naturally, so a file path looks like any other
// argument:
//
//	vibepat get all [bdf] ./lspci.txt
//
// The last argument is taken as the file when it names something readable OR it
// is the only argument. Otherwise every argument belongs to the grammar and the
// input comes from stdin. --file always wins, and when it is given every
// positional argument is grammar.
//
// The ambiguity is real but narrow: a grammar token that happens to name an
// existing file would be misread. --file removes the guess entirely.
func splitPositionals(args []string, explicitFile string) (grammar []string, path string, err error) {
	if explicitFile != "" {
		return args, explicitFile, nil
	}
	if len(args) == 0 {
		return nil, "", nil
	}

	last := args[len(args)-1]
	if len(args) == 1 {
		// A lone argument is a file only if it is one.
		if isReadableFile(last) {
			return nil, last, nil
		}
		return args, "", nil
	}

	if isReadableFile(last) {
		return args[:len(args)-1], last, nil
	}
	return args, "", nil
}

// isReadableFile reports whether path names an existing regular file.
func isReadableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// splitReplace parses an OLD=NEW rule. The separator is the first '=' so that
// the replacement text may itself contain '='.
func splitReplace(rule string) (string, string, error) {
	idx := strings.Index(rule, "=")
	if idx <= 0 {
		return "", "", fmt.Errorf("invalid --replace %q: want OLD=NEW with a non-empty OLD", rule)
	}
	return rule[:idx], rule[idx+1:], nil
}

// process chunks src into stanzas and converts each one into a MatchResult.
//
// Semantic token matching does not exist yet: Phase 2 establishes the stanza
// boundaries, and Phase 4 fills MatchedTokens using the registry. Until then
// every stanza is reported as a match, which is exactly the "get all" behavior.
//
// The context snapshot is taken before the current stanza's lines are pushed,
// so ContextLines always describes the lines preceding the stanza and never
// includes the stanza itself.
// processQueryWrite is the grammar's replace path: it reads the whole input,
// resolves the query's matches, builds a mutation plan, and runs it.
//
// Unlike the read-only paths this cannot stream, because a mutation plan is
// validated against, and replayed over, the entire original file.
func processQueryWrite(src *InputSource, q *Query, opts options, out, diffOut io.Writer, stdin io.Reader, stdoutIsTTY bool) error {
	lines, err := readAllLines(src)
	if err != nil {
		return err
	}
	stanzas, err := chunkLines(lines, opts.mode, opts.sampleSize)
	if err != nil {
		return err
	}

	results, err := SearchStanzas(q, stanzas, lineIndex(lines, stanzas))
	if err != nil {
		return err
	}
	return executeQueryWrite(q, results, lines, opts, out, diffOut, stdin, stdoutIsTTY)
}

// processReplace is the --replace path: it reads the whole input, builds a
// mutation plan, and hands it to the execution engine.
// processReplace builds a change plan from the literal replacement rules and
// hands it to the Phase 3 execution engine.
//
// It reads the whole input into memory because the write path needs the
// original text of every line in order to validate and re-render the file. The
// read-only path stays streaming.
func processReplace(src *InputSource, opts options, out, diffOut io.Writer, stdin io.Reader, stdoutIsTTY bool) error {
	lines, err := readAllLines(src)
	if err != nil {
		return err
	}

	rules := make([][2]string, 0, len(opts.replaces))
	for _, rule := range opts.replaces {
		old, new, err := splitReplace(rule)
		if err != nil {
			return err
		}
		rules = append(rules, [2]string{old, new})
	}

	// Rules compose in order: a line touched by two rules yields one mutation
	// whose ModifiedText reflects both.
	mutations := BuildSequentialMutations(lines, rules, opts.context)
	plan, err := NewChangePlan(opts.path, lines, mutations)
	if err != nil {
		return err
	}

	execMode, err := NormalizeExecMode(opts.execMode)
	if err != nil {
		return err
	}

	execOpts := ExecOptions{
		Mode:   execMode,
		SpotN:  opts.spotN,
		DryRun: opts.dryRun,
		Color:  !opts.noColor && stdoutIsTTY,
	}

	result, err := Execute(plan, execOpts, stdin, diffOut)
	if err != nil {
		return err
	}

	// A stdin-fed replace can never be written, which is a usage error rather
	// than a silent no-op.
	if plan.SourcePath == "" && result.Status == StatusApplied {
		return errNotAFile
	}

	// The interactive diff already went to the terminal; stdout carries the
	// summary exactly once.
	return WriteExecResult(out, result)
}

// readAllLines slurps the source into memory, preserving line order and
// stripping the trailing newline of each line.
func readAllLines(src *InputSource) ([]string, error) {
	sc := src.NewScanner()
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("%s: line %d exceeds the %d byte line limit", src.Name, len(lines)+1, maxScannerBuffer)
		}
		return nil, fmt.Errorf("read %s: %w", src.Name, err)
	}
	return lines, nil
}

// scanError converts a scanner failure into a descriptive error.
func scanError(src *InputSource, sc *VisualScanner, err error) error {
	// bufio.ErrTooLong means a single line exceeded maxScannerBuffer. Report the
	// position so the offending line can be found.
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("%s: line %d exceeds the %d byte line limit", src.Name, sc.lineNum+1, maxScannerBuffer)
	}
	return fmt.Errorf("read %s: %w", src.Name, err)
}

// stanzaStartLine derives the 1-based line on which stanza began, using the
// line counter after the stanza was consumed and the number of lines it
// contains.
func stanzaStartLine(sc *VisualScanner, stanza *Stanza) int {
	start := sc.lineNum - len(stanza.Lines) + 1
	if start < 1 {
		return 1
	}
	return start
}

// resolveTokens validates the requested token names and records whether the
// run should filter to matching stanzas only.
//
// With no --tokens flag every registered token is used and every stanza is
// reported, which is the historical "get all" behavior. With an explicit list,
// an unknown name is a usage error, and unmatched stanzas are dropped.
func resolveTokens(opts *options) error {
	if opts.tokenList != "" {
		opts.filterTokens = true
	}

	if opts.tokens == nil {
		opts.tokens = registry.Names()
		return nil
	}

	var unknown []string
	for _, name := range opts.tokens {
		if !registry.IsRegistered(name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown token(s) %s; available: %s",
			strings.Join(unknown, ", "), strings.Join(registry.Names(), ", "))
	}
	return nil
}

// writeJSON serializes results as a single, indented JSON array. Map key order
// inside each object is not stable in Go; consumers must not depend on it.
func writeJSON(out io.Writer, results []MatchResult) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	// Encode appends a trailing newline so shell prompts and CI logs stay tidy.
	if err := enc.Encode(results); err != nil {
		return fmt.Errorf("encode results: %w", err)
	}
	return nil
}

// processInteractive starts the REPL, reading the file once as the working copy.
func processInteractive(src *InputSource, opts options, out, diag io.Writer, stdin io.Reader, stdoutIsTTY bool) error {
	lines, err := readAllLines(src)
	if err != nil {
		return err
	}

	path := opts.path
	if src.Name == "<stdin>" {
		path = ""
	}

	return RunInteractive(path, lines, opts.mode, replEnv{
		In:         stdin,
		Out:        out,
		Diag:       diag,
		Color:      !opts.noColor && stdoutIsTTY,
		StdinIsTTY: isTerminal(os.Stdin),
	})
}

// chunkLines splits input lines into stanzas using the chosen mode.
func chunkLines(lines []string, mode string, sampleSize int) ([]*Stanza, error) {
	sc, err := NewVisualScanner(strings.NewReader(strings.Join(lines, "\n")), mode, sampleSize)
	if err != nil {
		return nil, err
	}

	var stanzas []*Stanza
	for {
		stanza, err := sc.NextStanza()
		if errors.Is(err, io.EOF) {
			return stanzas, nil
		}
		if err != nil {
			return nil, err
		}
		stanzas = append(stanzas, stanza)
	}
}

// lineIndex returns a function mapping a stanza index to the 1-based input line
// on which that stanza began.
//
// It walks the input once, skipping the blank separator lines the scanner drops,
// so every stanza is anchored to a real line number even when blank lines were
// removed between stanzas.
func lineIndex(lines []string, stanzas []*Stanza) []int {
	starts := make([]int, len(stanzas))
	cursor := 0

	for i, stanza := range stanzas {
		// Advance past blank separators.
		for cursor < len(lines) && strings.TrimSpace(lines[cursor]) == "" {
			cursor++
		}
		starts[i] = cursor + 1

		// Consume exactly as many lines as the stanza holds. The loop is not
		// bounded by len(lines) so the cursor still advances past the end of a
		// stanza whose lines ran out, which keeps later stanzas anchored.
		for j := 0; j < len(stanza.Lines); j++ {
			cursor++
		}
	}
	return starts
}

// executeQueryWrite turns a replace query into a change plan and runs it.
func executeQueryWrite(q *Query, results []SearchResult, lines []string, opts options, out, diffOut io.Writer, stdin io.Reader, stdoutIsTTY bool) error {
	mutations, err := BuildQueryMutations(q, results, lines)
	if err != nil {
		return err
	}

	plan, err := NewChangePlan(opts.path, lines, mutations)
	if err != nil {
		return err
	}

	execMode := ModeDefault
	switch {
	case q.HasModifier(ModifierYolo):
		execMode = ModeYOLO
	case q.HasModifier(ModifierSpot):
		execMode = ModeSpot
	}

	spotN := q.SpotN
	if spotN == 0 {
		spotN = opts.spotN
	}

	execOpts := ExecOptions{
		Mode:   execMode,
		SpotN:  spotN,
		DryRun: opts.dryRun || q.HasModifier(ModifierDryRun),
		Color:  !opts.noColor && !q.HasModifier(ModifierNoColor) && stdoutIsTTY,
	}

	result, err := Execute(plan, execOpts, stdin, diffOut)
	if err != nil {
		return err
	}
	if plan.SourcePath == "" && result.Status == StatusApplied {
		return errNotAFile
	}
	return WriteExecResult(out, result)
}
