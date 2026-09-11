package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/badvibecoder/vibepat/internal/registry"
)

// streamWriter emits MatchResult values as NDJSON: one JSON object per line,
// flushed as soon as it is produced.
//
// NDJSON rather than a single array is what keeps `tail -f`, `grep`, and `head`
// usable on a live stream. A single JSON array cannot be emitted until the last
// match is known, which defeats the point of a pipeline.
type streamWriter struct {
	w   *bufio.Writer
	enc *json.Encoder

	// written counts emitted objects, which is what the "first N" scope bounds.
	written int
	// err records the first write failure so a broken pipe stops the run rather
	// than being retried for every remaining stanza.
	err error
}

// newStreamWriter wraps w for NDJSON emission.
func newStreamWriter(w io.Writer) *streamWriter {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	// Compact output: one object per line, no indentation. HTML escaping is off
	// so device names and paths survive verbatim.
	enc.SetEscapeHTML(false)
	return &streamWriter{w: bw, enc: enc}
}

// emit writes one match as a single JSON line.
func (s *streamWriter) emit(m MatchResult) {
	if s.err != nil {
		return
	}
	normalizeResult(&m)
	if err := s.enc.Encode(m); err != nil {
		s.err = fmt.Errorf("write result: %w", err)
		return
	}
	s.written++
}

// Flush flushes buffered output, preferring a write error over a flush error
// because the first failure is the informative one.
func (s *streamWriter) Flush() error {
	if s.err != nil {
		return s.err
	}
	if err := s.w.Flush(); err != nil {
		return fmt.Errorf("flush results: %w", err)
	}
	return nil
}

// normalizeResult guarantees the map and slice fields are non-nil so they
// serialize as {} and [] rather than null.
func normalizeResult(m *MatchResult) {
	if m.MatchedTokens == nil {
		m.MatchedTokens = map[string][]string{}
	}
	if m.ContextLines == nil {
		m.ContextLines = []string{}
	}
}

// streamMatches is the default read-only path: chunk the input, match the
// selected tokens against each stanza, and emit each match immediately.
//
// Nothing but the preceding-context ring buffer is retained, so memory is bounded
// by --context rather than by input size.
func streamMatches(src *InputSource, opts options, out, diag io.Writer) error {
	sc, err := NewVisualScanner(src.Reader, opts.mode, opts.sampleSize)
	if err != nil {
		return err
	}
	if opts.mode == ModeAuto {
		fmt.Fprintf(diag, "vibepat: mode auto-detected as %q (%s)\n", sc.Mode(), src.Name)
	}

	sw := newStreamWriter(out)
	ring := NewRingBuffer(opts.context)

	for {
		stanza, err := sc.NextStanza()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A partially written stream is still flushed: the matches emitted
			// before the error are valid output.
			_ = sw.Flush()
			return scanError(src, sc, err)
		}

		matched := registry.MatchTokens(stanza.RawText, opts.tokens)
		// Without an explicit --tokens list every stanza is reported; with one,
		// only stanzas that matched something are.
		if !opts.filterTokens || len(matched) > 0 {
			result := NewMatchResult(PositionStanza, stanzaStartLine(sc, stanza), ring.GetContext())
			result.StanzaIndex = sc.stanzaIndex
			result.Stanza = stanza
			result.MatchedTokens = matched
			sw.emit(result)
		}

		pushStanza(ring, stanza)
	}

	return sw.Flush()
}

// streamQuery runs a read-only grammar query, emitting matches as NDJSON.
//
// Scopes are honored without buffering the whole input:
//
//   - all, first N, stanza N, today: decided per stanza as it arrives
//   - last N: a bounded buffer keeps only the final N matches
//   - drop: the complement is emitted as non-matching stanzas arrive
//
// Only "last N" retains anything, and it retains exactly N results.
func streamQuery(src *InputSource, q *Query, opts options, out, diag io.Writer) error {
	sc, err := NewVisualScanner(src.Reader, opts.mode, opts.sampleSize)
	if err != nil {
		return err
	}
	if opts.mode == ModeAuto {
		fmt.Fprintf(diag, "vibepat: mode auto-detected as %q (%s)\n", sc.Mode(), src.Name)
	}

	selectors, err := buildSelectors(q)
	if err != nil {
		return err
	}

	sw := newStreamWriter(out)

	// Context mirrors the --tokens path: a ring buffer holds the lines that
	// precede the current stanza, snapshotted before the stanza is pushed. A
	// "with context N" clause overrides the --context flag.
	contextSize := opts.context
	if q.HasContext {
		contextSize = q.ContextSize
	}
	ring := NewRingBuffer(contextSize)

	// "last N" holds back at most N results; everything else streams straight out.
	var held []MatchResult
	if q.Scope == ScopeLastN && q.ScopeN > 0 {
		held = make([]MatchResult, 0, q.ScopeN)
	}

	stanzaIdx := 0
	for {
		stanza, err := sc.NextStanza()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = sw.Flush()
			return scanError(src, sc, err)
		}

		stanzaIdx++

		// Snapshot the preceding lines before this stanza is pushed, so the
		// context never contains the match itself.
		context := ring.GetContext()
		matched := matchSelectors(selectors, stanza.RawText)

		if !stanzaSatisfies(q, selectors, matched, stanza.RawText, stanzaIdx) {
			if q.Action == ActionDrop {
				// A non-matching stanza is exactly what drop reports.
				sw.emit(queryResult(stanza, sc, stanzaIdx, nil, context))
			}
			pushStanza(ring, stanza)
			continue
		}

		if q.Action == ActionDrop {
			// Matched, so drop suppresses it.
			pushStanza(ring, stanza)
			continue
		}

		result := queryResult(stanza, sc, stanzaIdx, matched, context)

		if held != nil {
			held = appendHeld(held, result, q.ScopeN)
			continue
		}
		sw.emit(result)

		// "first N" and "stanza N" can stop as soon as the answer is complete,
		// which matters when reading an unbounded stream such as `tail -f`.
		if q.Scope == ScopeFirstN && sw.written >= q.ScopeN {
			return sw.Flush()
		}
		if q.Scope == ScopeStanza && stanzaIdx >= q.ScopeN {
			return sw.Flush()
		}

		pushStanza(ring, stanza)
	}

	for _, result := range held {
		sw.emit(result)
	}
	return sw.Flush()
}

// appendHeld appends result to a bounded "last N" buffer, dropping the oldest
// entry once the buffer is full.
func appendHeld(held []MatchResult, result MatchResult, n int) []MatchResult {
	if len(held) < n {
		return append(held, result)
	}
	// Shift left and overwrite the last slot. N is small, so the copy is cheap
	// and this avoids a second buffer type.
	copy(held, held[1:])
	held[len(held)-1] = result
	return held
}

// stanzaSatisfies applies the target match, the conjunctive requirement, and the
// scope predicate to one stanza.
func stanzaSatisfies(q *Query, selectors []*selector, matched map[string][]string, text string, stanzaIdx int) bool {
	if len(selectors) > 0 {
		if len(matched) == 0 {
			return false
		}
		if q.RequireAll {
			for _, sel := range selectors {
				if len(matched[sel.token]) == 0 {
					return false
				}
			}
		}
	}

	switch q.Scope {
	case ScopeToday:
		return containsAnyDate(text, todayPatterns())
	case ScopeStanza:
		return stanzaIdx == q.ScopeN
	}
	return true
}

// queryResult assembles a MatchResult for a stanza.
//
// context holds the lines preceding the stanza, oldest first. It is supplied by
// the caller's ring buffer so that the grammar path and the --tokens path embed
// identical context for identical input.
func queryResult(stanza *Stanza, sc *VisualScanner, idx int, matched map[string][]string, context []string) MatchResult {
	result := NewMatchResult(PositionStanza, stanzaStartLine(sc, stanza), context)
	result.StanzaIndex = idx
	result.Stanza = stanza
	if matched != nil {
		result.MatchedTokens = matched
	}
	return result
}

// pushStanza adds a stanza's lines to the context ring buffer, in order, so the
// next match sees them as preceding lines.
func pushStanza(ring *RingBuffer, stanza *Stanza) {
	for _, line := range stanza.Lines {
		ring.Push(line)
	}
}

// matchSelectors runs every selector against a stanza's text.
func matchSelectors(selectors []*selector, text string) map[string][]string {
	if len(selectors) == 0 {
		return nil
	}
	matched := map[string][]string{}
	for _, sel := range selectors {
		values := sel.matchStanza(text)
		if len(values) > 0 {
			matched[sel.token] = append(matched[sel.token], values...)
		}
	}
	return matched
}
