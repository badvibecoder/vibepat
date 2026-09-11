package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// MockReplacement is the placeholder text Phase 3's demonstrator searches for.
// Phase 5's grammar supplies real patterns instead.
const MockReplacement = "MOCK_REPLACE"

// MockReplacementWith is what MockReplacement is rewritten to.
const MockReplacementWith = "MOCK_SUCCESS"

// Mutation records one in-memory text change before it is committed to disk.
//
// LineNumber is always a 1-based line in the *original* input. That is what
// makes a set of mutations replayable against a freshly read file, and what
// lets a diff be rendered without re-running the matchers.
type Mutation struct {
	// LineNumber is the 1-based line the change applies to.
	LineNumber int `json:"line_number"`
	// OriginalText is the text before replacement.
	OriginalText string `json:"original_text"`
	// ModifiedText is the text after replacement.
	ModifiedText string `json:"modified_text"`
	// ContextLines holds surrounding lines shown in a diff hunk.
	ContextLines []string `json:"context_lines"`
}

// ChangePlan is a validated, replayable set of mutations against one input.
//
// It is the unit of work handed to the execution handlers. Building a plan
// never touches disk; only the execution handlers write.
type ChangePlan struct {
	// SourcePath is the file the mutations apply to, or "" when the input came
	// from stdin (in which case there is nothing to write).
	SourcePath string
	// OriginalLines is the full input, split into lines, so that Apply does not
	// have to re-read or re-scan anything.
	OriginalLines []string
	// Mutations are the changes, in ascending LineNumber order.
	Mutations []Mutation
}

// Validate checks that the plan is internally consistent and safe to apply. A
// plan that fails validation must never be written, because overlapping or
// out-of-range mutations would silently corrupt the file.
func (p *ChangePlan) Validate() error {
	if len(p.Mutations) == 0 {
		return nil
	}

	seen := make(map[int]bool, len(p.Mutations))
	for i, m := range p.Mutations {
		if m.LineNumber < 1 || m.LineNumber > len(p.OriginalLines) {
			return fmt.Errorf("mutation %d targets line %d, outside the valid range 1..%d",
				i, m.LineNumber, len(p.OriginalLines))
		}
		if seen[m.LineNumber] {
			return fmt.Errorf("mutation %d targets line %d, which is already changed by an earlier mutation",
				i, m.LineNumber)
		}
		seen[m.LineNumber] = true

		// The original text must still match, or the plan was built against a
		// different revision of the input.
		if p.OriginalLines[m.LineNumber-1] != m.OriginalText {
			return fmt.Errorf("mutation %d expects line %d to be %q, but it is %q",
				i, m.LineNumber, m.OriginalText, p.OriginalLines[m.LineNumber-1])
		}
	}
	return nil
}

// Apply returns a copy of the input lines with every mutation applied. It does
// not modify the plan or its OriginalLines, so a plan can be applied repeatedly
// and compared against a dry run.
func (p *ChangePlan) Apply() []string {
	out := make([]string, len(p.OriginalLines))
	copy(out, p.OriginalLines)

	for _, m := range p.Mutations {
		if m.LineNumber < 1 || m.LineNumber > len(out) {
			continue
		}
		out[m.LineNumber-1] = m.ModifiedText
	}
	return out
}

// NewChangePlan builds a plan for path (or stdin when path is empty) from the
// already-split input lines and the given mutations, sorting them by line
// number and validating the result.
func NewChangePlan(path string, originalLines []string, mutations []Mutation) (*ChangePlan, error) {
	plan := &ChangePlan{
		SourcePath:    path,
		OriginalLines: originalLines,
		Mutations:     mutations,
	}
	sort.SliceStable(plan.Mutations, func(i, j int) bool {
		return plan.Mutations[i].LineNumber < plan.Mutations[j].LineNumber
	})
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return plan, nil
}

// MockReplaceStanza implements the Phase 3 demonstrator replacement: it rewrites
// every occurrence of MockReplacement to MockReplacementWith within a stanza and
// returns one Mutation per changed line.
//
// startLine is the 1-based line on which the stanza began, so the returned
// mutations carry positions in the original input rather than stanza-relative
// ones. A stanza with no occurrences yields no mutations.
func MockReplaceStanza(stanza *Stanza, startLine int, context []string) []Mutation {
	if stanza == nil {
		return nil
	}

	var mutations []Mutation
	for i, line := range stanza.Lines {
		if !strings.Contains(line, MockReplacement) {
			continue
		}
		mutations = append(mutations, Mutation{
			LineNumber:   startLine + i,
			OriginalText: line,
			ModifiedText: strings.ReplaceAll(line, MockReplacement, MockReplacementWith),
			ContextLines: copyStrings(context),
		})
	}
	return mutations
}

// ReplaceAllLiteral builds mutations that rewrite every occurrence of old with
// new across the input lines. It is the literal-target form of the grammar
// (`replace ['OLD'] with ['NEW']`), and the engine Phase 5's semantic targets
// feed into.
//
// The match is a plain substring replacement, not a regex or wildcard; wildcard
// capture is a Phase 5 concern. For several rules over one input, use
// BuildSequentialMutations so the rules compose instead of colliding.
func ReplaceAllLiteral(lines []string, old, new string, contextSize int) []Mutation {
	return BuildSequentialMutations(lines, [][2]string{{old, new}}, contextSize)
}

// BuildSequentialMutations applies a series of literal replacement rules in
// order and returns one mutation per changed line, with OriginalText taken from
// the true original input and ModifiedText reflecting every rule that applied.
//
// This is the composition the CLI needs: `--replace a=b --replace b=c` must not
// silently drop one of the rules just because both touch the same line.
func BuildSequentialMutations(lines []string, rules [][2]string, contextSize int) []Mutation {
	working := copyStrings(lines)
	// changed records the working text per line once that line has diverged.
	changed := make(map[int]string)

	for _, rule := range rules {
		old, new := rule[0], rule[1]
		if old == "" {
			continue
		}
		for i, line := range working {
			if !strings.Contains(line, old) {
				continue
			}
			updated := strings.ReplaceAll(line, old, new)
			working[i] = updated
			changed[i+1] = updated
		}
	}

	if len(changed) == 0 {
		return nil
	}

	lineNumbers := make([]int, 0, len(changed))
	for n := range changed {
		lineNumbers = append(lineNumbers, n)
	}
	sort.Ints(lineNumbers)

	mutations := make([]Mutation, 0, len(lineNumbers))
	for _, n := range lineNumbers {
		mutations = append(mutations, Mutation{
			LineNumber:   n,
			OriginalText: lines[n-1],
			ModifiedText: changed[n],
			ContextLines: precedingLines(lines, n-1, contextSize),
		})
	}
	return mutations
}

// precedingLines returns up to n lines immediately before index i, oldest first.
func precedingLines(lines []string, i, n int) []string {
	if n <= 0 {
		return nil
	}
	start := i - n
	if start < 0 {
		start = 0
	}
	return copyStrings(lines[start:i])
}

// copyStrings returns a fresh copy so callers cannot alias shared slices.
func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// SampleRandom returns a random subset of mutations of size at most n, using
// the supplied source of randomness so tests can make it deterministic.
//
// The input slice is not modified, and the returned slice preserves ascending
// line order so a spot-check reads like a coherent file excerpt rather than a
// shuffled mess. That is why this samples *then* sorts, instead of shuffling in
// place.
func SampleRandom(mutations []Mutation, n int, rng *rand.Rand) []Mutation {
	if n <= 0 || len(mutations) == 0 {
		return nil
	}
	if n >= len(mutations) {
		return copyMutations(mutations)
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(rand.Int63()))
	}

	// Partial Fisher-Yates over an index slice: O(len) time, O(len) space, and
	// unlike a full shuffle it does no work for the elements we discard.
	idx := make([]int, len(mutations))
	for i := range idx {
		idx[i] = i
	}
	for i := 0; i < n; i++ {
		j := i + rng.Intn(len(idx)-i)
		idx[i], idx[j] = idx[j], idx[i]
	}

	chosen := make([]Mutation, 0, n)
	for _, i := range idx[:n] {
		chosen = append(chosen, mutations[i])
	}
	sort.Slice(chosen, func(a, b int) bool {
		return chosen[a].LineNumber < chosen[b].LineNumber
	})
	return chosen
}

// copyMutations deep-copies a mutation slice, including ContextLines, so callers
// can retain the result independently of the source.
func copyMutations(in []Mutation) []Mutation {
	out := make([]Mutation, len(in))
	for i, m := range in {
		out[i] = m
		out[i].ContextLines = copyStrings(m.ContextLines)
	}
	return out
}

// errNotAFile is returned when a plan has no writable source path.
var errNotAFile = errors.New("input came from stdin, so there is no file to modify")
