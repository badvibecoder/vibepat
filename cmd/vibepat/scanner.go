package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// headerAnchor recognizes the shapes Mode B anchors on:
//
//   - INI / config sections: "[defaults]", "[Interface]"
//   - PCI BDF identifiers as emitted by lspci, with or without a domain:
//     "0000:41:00.0", "41:00.0", "0000:06:00.0"
//   - Compiler/grep-style "file:line:" prefixes, which require a path separator
//     so a bare "word:42:" is not mistaken for one.
//
// The BDF alternative requires either a four-hex-digit domain or a
// device.function suffix. That is deliberate: a bare "ab:cd" would match
// timestamps and arbitrary hex pairs, which is exactly the false-positive class
// the visual scanner must avoid.
var headerAnchor = regexp.MustCompile(
	`^\[[A-Za-z0-9_.-]+\]` +
		`|^(?:[0-9A-Fa-f]{4}:)?[0-9A-Fa-f]{2}:[0-9A-Fa-f]{2}\.[0-7]\b` +
		`|^[A-Za-z0-9_.+-]*/[A-Za-z0-9_./+-]*:[0-9]+:`,
)

// Visual chunking modes. These are the user-facing values of the --mode flag.
const (
	// ModeAuto sniffs the head of the stream and picks one of the concrete
	// modes below. This is the default.
	ModeAuto = "auto"
	// ModeStream treats every line as its own stanza.
	ModeStream = "stream"
	// ModeHeader anchors stanzas on header lines (INI sections, lspci BDFs).
	ModeHeader = "header"
	// ModeIndent anchors stanzas on indentation ridges (YAML, `ip -d a`).
	ModeIndent = "indent"
)

// DefaultSampleSize is how many leading lines ModeAuto inspects before
// committing to a concrete mode.
const DefaultSampleSize = 100

// autoHeaderRatio is the minimum fraction of sampled lines that must look like
// headers before ModeHeader is chosen.
//
// The threshold is low on purpose. Real `lspci -vv` output is ~2.5% headers
// because each device contributes one header line and dozens of indented
// capability lines, yet it is unambiguously a header-delimited document. A
// threshold anywhere near the 10% that INI (27%) and YAML (17%) produce would
// misclassify it. One percent still comfortably excludes prose, where headers
// essentially never occur, and the absolute floor of two header lines guards the
// short-input case.
const autoHeaderRatio = 0.01

// autoIndentRatio is the minimum fraction of sampled lines that must be
// indented before ModeIndent is chosen.
const autoIndentRatio = 0.20

// minHeaderRows is the absolute floor of header-looking lines required, so a
// short stream with one coincidental match is not misclassified.
const minHeaderRows = 2

// stripANSI removes SGR color/format escape sequences (CSI ... m) so that
// `lspci -v | colordiff` or `journalctl` output with color still matches the
// anchors, which are all anchored on columns.
func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
				j++
			}
			if j < len(s) {
				j++ // consume the final byte
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// leadingSpaces returns the number of leading space and tab characters. A tab
// counts as one column; mixing the two is rare enough in the inputs vibepat
// targets that normalizing would cost more correctness than it buys.
func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}

// VisualScanner chunks a text stream into logical Stanza values using visual
// heuristics rather than a grammar. It deliberately mirrors the shape of
// bufio.Scanner: call NextStanza until it returns io.EOF.
//
// A VisualScanner is not safe for concurrent use.
type VisualScanner struct {
	mode string

	sc *bufio.Scanner

	// buffer holds lines that have been read from the underlying scanner but not
	// yet emitted. It is used both for the ModeAuto sniff and for one-line
	// lookahead in header mode. Reading every line through this buffer keeps a
	// single source of truth for line position.
	buffer []string

	// lineNum is the number of lines consumed from the buffer so far.
	lineNum int

	// stanzaIndex counts emitted stanzas, 1-based.
	stanzaIndex int

	// err is the first non-EOF error encountered, returned after all buffered
	// lines have been drained.
	err error
}

// NewVisualScanner constructs a scanner over r in the requested mode.
//
// mode accepts the Mode* constants, and additionally "ini" as an alias for
// ModeHeader. An unrecognized mode is an error rather than a silent fallback,
// because guessing wrong here silently changes every downstream result.
//
// When mode is ModeAuto, up to sampleSize leading lines are buffered and scored
// before a concrete mode is chosen. A sampleSize of zero or less uses
// DefaultSampleSize.
func NewVisualScanner(r io.Reader, mode string, sampleSize int) (*VisualScanner, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, DefaultScannerBuffer), maxScannerBuffer)

	resolved, err := normalizeMode(mode)
	if err != nil {
		return nil, err
	}

	vs := &VisualScanner{mode: resolved, sc: sc}

	if resolved == ModeAuto {
		vs.fill(sampleSize)
		vs.mode = detectMode(vs.buffer)
	}
	return vs, nil
}

// normalizeMode validates a user-supplied mode string.
func normalizeMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeAuto:
		return ModeAuto, nil
	case ModeStream:
		return ModeStream, nil
	case ModeHeader, "ini":
		return ModeHeader, nil
	case ModeIndent:
		return ModeIndent, nil
	default:
		return "", fmt.Errorf("unknown mode %q (want auto, stream, header, or indent)", mode)
	}
}

// Mode reports the concrete mode in use. When the scanner was constructed with
// ModeAuto, this is the mode detection settled on, not "auto".
func (vs *VisualScanner) Mode() string { return vs.mode }

// fill reads up to n lines into the buffer, stopping early at EOF. A n of zero
// or less uses DefaultSampleSize.
func (vs *VisualScanner) fill(n int) {
	if n <= 0 {
		n = DefaultSampleSize
	}
	for len(vs.buffer) < n && vs.sc.Scan() {
		vs.buffer = append(vs.buffer, stripANSI(vs.sc.Text()))
	}
	if err := vs.sc.Err(); err != nil && vs.err == nil {
		vs.err = err
	}
}

// next returns the next buffered line, reading from the underlying scanner when
// the buffer is exhausted. ok is false at end of input or on error.
func (vs *VisualScanner) next() (string, bool) {
	if len(vs.buffer) > 0 {
		line := vs.buffer[0]
		vs.buffer = vs.buffer[1:]
		vs.lineNum++
		return line, true
	}
	if vs.err != nil {
		return "", false
	}
	if vs.sc.Scan() {
		vs.lineNum++
		return stripANSI(vs.sc.Text()), true
	}
	if err := vs.sc.Err(); err != nil {
		vs.err = err
	}
	return "", false
}

// peek returns the next buffered line without consuming it.
func (vs *VisualScanner) peek() (string, bool) {
	if len(vs.buffer) > 0 {
		return vs.buffer[0], true
	}
	if vs.err != nil {
		return "", false
	}
	if vs.sc.Scan() {
		vs.buffer = append(vs.buffer, stripANSI(vs.sc.Text()))
		return vs.buffer[0], true
	}
	if err := vs.sc.Err(); err != nil {
		vs.err = err
	}
	return "", false
}

// NextStanza returns the next logical stanza. It returns io.EOF when the input
// is exhausted, and any underlying read error once the buffer is drained.
//
// Blank lines are separators rather than content in the anchored modes, so a
// blank line is never allowed to begin a stanza there. In stream mode a blank
// line is a line like any other, and only blanks trailing the end of input are
// discarded.
func (vs *VisualScanner) NextStanza() (*Stanza, error) {
	for {
		line, ok := vs.next()
		if !ok {
			if vs.err != nil {
				return nil, vs.err
			}
			return nil, io.EOF
		}

		if strings.TrimSpace(line) == "" {
			// A blank that closed the previous stanza, or that trails the whole
			// input, starts nothing.
			if vs.mode != ModeStream || vs.onlyBlanksRemain() {
				continue
			}
		}

		var lines []string
		switch vs.mode {
		case ModeHeader:
			lines = vs.scanHeader(line)
		case ModeIndent:
			lines = vs.scanIndent(line)
		default:
			lines = []string{line}
		}

		vs.stanzaIndex++
		return newStanza(vs.mode, lines), nil
	}
}

// onlyBlanksRemain reports whether the rest of the input consists solely of
// blank lines. It consumes nothing.
func (vs *VisualScanner) onlyBlanksRemain() bool {
	for i := 0; i < len(vs.buffer); i++ {
		if strings.TrimSpace(vs.buffer[i]) != "" {
			return false
		}
	}
	for i := 0; i < maxTrailingLookahead; i++ {
		line, ok := vs.peek()
		if !ok {
			return true
		}
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	// More blanks than we care to look through: treat as trailing.
	return true
}

// maxTrailingLookahead bounds how far onlyBlanksRemain scans for a non-blank
// line before declaring the remainder empty.
const maxTrailingLookahead = 64

// scanHeader implements Mode B. line is the already-consumed header candidate,
// which is always included even if it turns out not to look like a header (that
// happens for leading prelude text).
//
// The stanza closes on a line that matches a header pattern, or on a blank line
// whose next non-blank line is unindented. That lookahead rule is what keeps
// real `lspci -vvv` device blocks intact: lspci separates capability groups with
// blank lines, but the following line is indented, so the blank stays inside the
// device stanza.
func (vs *VisualScanner) scanHeader(line string) []string {
	lines := []string{line}

	for {
		next, ok := vs.next()
		if !ok {
			return lines
		}

		if isHeaderLine(next) {
			vs.pushBack(next)
			return lines
		}

		if strings.TrimSpace(next) == "" {
			if vs.blankEndsStanza() {
				// The blank itself is a separator, not part of the stanza.
				return lines
			}
			lines = append(lines, next)
			continue
		}

		lines = append(lines, next)
	}
}

// blankEndsStanza reports whether the current blank line terminates the stanza.
// It consumes nothing: the lookahead line is returned to the buffer. Blank lines
// at end of input do terminate the stanza, and the next NextStanza call reports
// io.EOF.
func (vs *VisualScanner) blankEndsStanza() bool {
	next, ok := vs.peek()
	if !ok {
		return true
	}
	return strings.TrimSpace(next) == "" || leadingSpaces(next) == 0
}

// pushBack returns a line to the front of the buffer and rewinds the line
// counter so it is not double-counted.
func (vs *VisualScanner) pushBack(line string) {
	vs.buffer = append([]string{line}, vs.buffer...)
	vs.lineNum--
}

// scanIndent implements Mode C. line is the already-consumed ridge line; its
// indentation sets the level, and the stanza runs until a line is seen with
// indentation at or below that level.
func (vs *VisualScanner) scanIndent(line string) []string {
	level := leadingSpaces(line)
	lines := []string{line}

	for {
		next, ok := vs.next()
		if !ok {
			return lines
		}
		if leadingSpaces(next) <= level {
			vs.pushBack(next)
			return lines
		}
		lines = append(lines, next)
	}
}

// newStanza assembles a Stanza and trims trailing blank lines, which are
// separators rather than content.
func newStanza(boundary string, lines []string) *Stanza {
	for len(lines) > 1 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return &Stanza{
		Lines:        lines,
		RawText:      strings.Join(lines, "\n"),
		BoundaryType: boundary,
	}
}

// isHeaderLine reports whether line starts a new stanza under Mode B. Leading
// whitespace is tolerated so that BDF headers indented by lspci remain
// detectable, but indentation is never required.
func isHeaderLine(line string) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	return headerAnchor.MatchString(strings.TrimSpace(line))
}

// detectMode scores the sampled lines and picks a concrete mode. Header
// structure wins over indentation because a header-delimited document usually
// also contains indented lines, whereas an indentation-ridge document rarely
// contains header anchors.
func detectMode(sample []string) string {
	if len(sample) == 0 {
		return ModeStream
	}

	var headers, indented int
	for _, line := range sample {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if isHeaderLine(line) {
			headers++
			continue
		}
		if leadingSpaces(line) > 0 {
			indented++
		}
	}

	total := float64(len(sample))
	if headers >= minHeaderRows && float64(headers)/total >= autoHeaderRatio {
		return ModeHeader
	}
	if float64(indented)/total >= autoIndentRatio {
		return ModeIndent
	}
	return ModeStream
}
