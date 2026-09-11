package main

import (
	"io"
	"strings"
	"testing"
)

// collect drains a scanner and returns every stanza. It fails the test on any
// error other than io.EOF.
func collect(t *testing.T, sc *VisualScanner) []*Stanza {
	t.Helper()

	var stanzas []*Stanza
	for {
		stanza, err := sc.NextStanza()
		if err == io.EOF {
			return stanzas
		}
		if err != nil {
			t.Fatalf("NextStanza: %v", err)
		}
		stanzas = append(stanzas, stanza)
	}
}

// newScanner builds a scanner for tests, failing on construction errors.
func newScanner(t *testing.T, input, mode string) *VisualScanner {
	t.Helper()

	sc, err := NewVisualScanner(strings.NewReader(input), mode, 0)
	if err != nil {
		t.Fatalf("NewVisualScanner(%q): %v", mode, err)
	}
	return sc
}

// --- Mode A: Stream -------------------------------------------------------

// TestStreamYieldsOneLinePerStanza is the Mode A gate.
func TestStreamYieldsOneLinePerStanza(t *testing.T) {
	sc := newScanner(t, "alpha\nbeta\ngamma\n", ModeStream)
	got := collect(t, sc)

	if len(got) != 3 {
		t.Fatalf("got %d stanzas, want 3", len(got))
	}
	for i, want := range []string{"alpha", "beta", "gamma"} {
		if len(got[i].Lines) != 1 || got[i].Lines[0] != want {
			t.Errorf("stanza %d = %v, want [%s]", i, got[i].Lines, want)
		}
		if got[i].BoundaryType != BoundaryStream {
			t.Errorf("stanza %d BoundaryType = %q, want %q", i, got[i].BoundaryType, BoundaryStream)
		}
	}
}

// TestStreamPreservesBlankLines verifies stream mode does not swallow blank
// lines, since each line is independently significant.
func TestStreamPreservesBlankLines(t *testing.T) {
	sc := newScanner(t, "a\n\nb\n", ModeStream)
	got := collect(t, sc)

	if len(got) != 3 {
		t.Fatalf("got %d stanzas, want 3", len(got))
	}
	if got[1].Lines[0] != "" {
		t.Errorf("middle stanza = %v, want a single empty line", got[1].Lines)
	}
}

// TestStreamEmptyInput verifies clean EOF on empty input.
func TestStreamEmptyInput(t *testing.T) {
	sc := newScanner(t, "", ModeStream)
	if got := collect(t, sc); len(got) != 0 {
		t.Fatalf("got %d stanzas, want 0", len(got))
	}
}

// mockLspciVV is a compact stand-in for real `lspci -vv` output, which is
// dominated by indented capability lines: one device header per many
// continuations. The header density here is deliberately low so the
// auto-detection threshold is exercised against realistic input rather than the
// header-dense fixtures used for the grouping tests.
const mockLspciVV = `00:00.0 Host bridge: Advanced Micro Devices, Inc. [AMD]
	Subsystem: Advanced Micro Devices, Inc. [AMD]
	Control: I/O- Mem- BusMaster- SpecCycle- MemWINV- VGASnoop- ParErr-
	Status: Cap- 66MHz- UDF- FastB2B- ParErr- DEVSEL=fast >TAbort- <TAbort-
	Latency: 0
	Interrupts: pin B disabled, MSI(X) routed to IRQ 25
	Capabilities: [40] Secure device <?>
	Capabilities: [64] MSI: Enable+ Count=1/4 Maskable- 64bit+
		Address: 00000000fee08000  Data: 0020
	Capabilities: [74] HyperTransport: MSI Mapping Enable+ Fixed+
	Kernel driver in use: amd_host
	Kernel modules: amd_host

00:01.1 PCI bridge: Advanced Micro Devices, Inc. [AMD] GPP Bridge
	Subsystem: Advanced Micro Devices, Inc. [AMD] Device 1453
	Control: I/O+ Mem+ BusMaster+ SpecCycle- MemWINV- VGASnoop- ParErr-
	Status: Cap+ 66MHz- UDF- FastB2B- ParErr- DEVSEL=fast >TAbort- <TAbort-
	Latency: 0, Cache Line Size: 64 bytes
	Interrupts: MSI(X) routed to IRQ 26
	Bus: primary=00, secondary=01, subordinate=01, sec-latency=0
	I/O behind bridge: 0000f000-00000fff [disabled]
	Memory behind bridge: fce00000-fcefffff [size=1M]
	Capabilities: [58] Express Upstream Port, MSI 00
		LnkCap: Port #0, Speed 16GT/s, Width x16, ASPM L1
		LnkSta: Speed 16GT/s, Width x16
	Capabilities: [a0] Power Management version 3
	Kernel driver in use: pcieport

00:01.2 PCI bridge: Advanced Micro Devices, Inc. [AMD] GPP Bridge
	Subsystem: Advanced Micro Devices, Inc. [AMD] Device 1453
	Control: I/O+ Mem+ BusMaster+ SpecCycle- MemWINV- VGASnoop- ParErr-
	Status: Cap+ 66MHz- UDF- FastB2B- ParErr- DEVSEL=fast >TAbort- <TAbort-
	Latency: 0, Cache Line Size: 64 bytes
	Interrupts: MSI(X) routed to IRQ 27
	Bus: primary=00, secondary=02, subordinate=02, sec-latency=0
	I/O behind bridge: 0000f000-00000fff [disabled]
	Memory behind bridge: fcf00000-fcffffff [size=1M]
	Capabilities: [58] Express Upstream Port, MSI 00
		LnkCap: Port #0, Speed 32GT/s, Width x4, ASPM L1
		LnkSta: Speed 2.5GT/s, Width x4 (downgraded)
	Capabilities: [a0] Power Management version 3
	Kernel driver in use: pcieport
`

// TestAutoDetectRealisticLspciHeaderDensity is the regression gate for a real
// misclassification found against a 1462-line root `lspci -vv`: with a 10%
// header-ratio threshold, that file was detected as indent mode.
func TestAutoDetectRealisticLspciHeaderDensity(t *testing.T) {
	sc, err := NewVisualScanner(strings.NewReader(mockLspciVV), ModeAuto, 0)
	if err != nil {
		t.Fatalf("NewVisualScanner: %v", err)
	}
	if got := sc.Mode(); got != ModeHeader {
		t.Errorf("detected %q, want %q for lspci-shaped input", got, ModeHeader)
	}

	// The header density of this fixture must stay well below the old 10%
	// threshold, or the test would stop guarding anything.
	lines := strings.Split(mockLspciVV, "\n")
	var headers int
	for _, l := range lines {
		if isHeaderLine(l) {
			headers++
		}
	}
	if ratio := float64(headers) / float64(len(lines)); ratio >= 0.10 {
		t.Fatalf("fixture header ratio is %.3f, which no longer exercises the low-density case", ratio)
	}
}

// TestLspciGroupingIsModeIndependent documents that header and indent modes
// agree on well-formed lspci output, so the detection choice is not load-bearing
// for this input.
func TestLspciGroupingIsModeIndependent(t *testing.T) {
	headerStanzas := collect(t, newScanner(t, mockLspciVV, ModeHeader))
	indentStanzas := collect(t, newScanner(t, mockLspciVV, ModeIndent))

	if len(headerStanzas) != len(indentStanzas) {
		t.Fatalf("header mode gave %d stanzas, indent gave %d",
			len(headerStanzas), len(indentStanzas))
	}
	if len(headerStanzas) != 3 {
		t.Fatalf("got %d stanzas, want 3 devices", len(headerStanzas))
	}
	for i := range headerStanzas {
		a := strings.Join(headerStanzas[i].Lines, "\n")
		b := strings.Join(indentStanzas[i].Lines, "\n")
		if a != b {
			t.Errorf("stanza %d differs between modes:\nheader: %q\nindent: %q", i, a, b)
		}
	}
}

// --- Mode B: Header Anchor ------------------------------------------------

// mockLspci is real-shaped `lspci -v` output. Note the blank line separating
// capability groups inside the first device: the lookahead rule must keep it,
// because the following line is indented.
const mockLspci = `00:00.0 Host bridge: Intel Corporation Device 09ab
	Subsystem: Dell Device 09ab
	Flags: fast devsel
	Capabilities: [e0] Vendor Specific Information: Len=0c <?>

00:02.0 VGA compatible controller: Intel Corporation Device 46a6
	Subsystem: Dell Device 0bd0
	Flags: bus master, fast devsel, latency 0
	Memory at 6050000000 (64-bit, non-prefetchable) [size=16M]

00:1f.6 Ethernet controller: Intel Corporation Device 15f9
	Subsystem: Dell Device 0a2b
	Flags: bus master, fast devsel, latency 0
	Capabilities: [c8] Power Management version 3
	Kernel driver in use: e1000e
`

// TestHeaderSplitsOnColumnZeroBDF is the Mode B gate: each BDF header starts a
// stanza, indented capabilities stay with their device, and the blank line
// inside the first device does not truncate it.
func TestHeaderSplitsOnColumnZeroBDF(t *testing.T) {
	sc := newScanner(t, mockLspci, ModeHeader)
	got := collect(t, sc)

	if len(got) != 3 {
		t.Fatalf("got %d stanzas, want 3 devices:\n%s", len(got), dump(got))
	}

	// Device 1: header + 3 indented lines + the internal blank line.
	wantFirst := []string{
		"00:00.0 Host bridge: Intel Corporation Device 09ab",
		"\tSubsystem: Dell Device 09ab",
		"\tFlags: fast devsel",
		"\tCapabilities: [e0] Vendor Specific Information: Len=0c <?>",
	}
	if len(got[0].Lines) != len(wantFirst) {
		t.Fatalf("first stanza has %d lines, want %d:\n%s", len(got[0].Lines), len(wantFirst), dump(got))
	}
	for i := range wantFirst {
		if got[0].Lines[i] != wantFirst[i] {
			t.Errorf("first stanza line %d = %q, want %q", i, got[0].Lines[i], wantFirst[i])
		}
	}

	for i, want := range []string{"00:00.0", "00:02.0", "00:1f.6"} {
		if !strings.HasPrefix(got[i].Lines[0], want) {
			t.Errorf("stanza %d starts with %q, want prefix %q", i, got[i].Lines[0], want)
		}
		if got[i].BoundaryType != BoundaryHeader {
			t.Errorf("stanza %d BoundaryType = %q, want %q", i, got[i].BoundaryType, BoundaryHeader)
		}
	}

	// The kernel driver line must remain attached to the third device.
	last := got[2]
	if !strings.Contains(last.RawText, "Kernel driver in use: e1000e") {
		t.Errorf("last stanza lost its capability tail:\n%s", last.RawText)
	}
}

// TestHeaderClosesOnUnindentedBlankRule verifies the exact rule chosen for
// blank lines: a blank line closes the stanza when the next non-blank line is
// unindented, and does not when the next non-blank line is indented.
func TestHeaderClosesOnUnindentedBlankRule(t *testing.T) {
	const closes = "00:00.0 Alpha\ttabbed: value\n\nplain text not indented\n"
	sc := newScanner(t, closes, ModeHeader)
	got := collect(t, sc)

	if len(got) != 2 {
		t.Fatalf("got %d stanzas, want 2:\n%s", len(got), dump(got))
	}
	if got[0].Lines[len(got[0].Lines)-1] == "" {
		t.Errorf("closing stanza retained a trailing blank line: %v", got[0].Lines)
	}
	if got[1].Lines[0] != "plain text not indented" {
		t.Errorf("second stanza = %v, want the unindented text", got[1].Lines)
	}
}

// TestHeaderKeepsBlankBeforeIndented verifies the complementary case: a blank
// line followed by an indented line stays inside the stanza.
func TestHeaderKeepsBlankBeforeIndented(t *testing.T) {
	const keeps = "00:00.0 Alpha\n\tone\n\n\ttwo\n"
	sc := newScanner(t, keeps, ModeHeader)
	got := collect(t, sc)

	if len(got) != 1 {
		t.Fatalf("got %d stanzas, want 1:\n%s", len(got), dump(got))
	}
	want := []string{"00:00.0 Alpha", "\tone", "", "\ttwo"}
	if len(got[0].Lines) != len(want) {
		t.Fatalf("lines = %v, want %v", got[0].Lines, want)
	}
	for i := range want {
		if got[0].Lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[0].Lines[i], want[i])
		}
	}
}

// TestHeaderINI verifies the INI section shape, including the bracket-anchor
// false-positive guard: an IPv6 address must not be mistaken for a section.
func TestHeaderINI(t *testing.T) {
	const ini = `[defaults]
key = value
other = 1

[Interface]
Address = 10.0.0.1
MTU = 1500

[Peer]
AllowedIPs = 2001:db8::1/128, fe80::/64
`
	sc := newScanner(t, ini, ModeHeader)
	got := collect(t, sc)

	if len(got) != 3 {
		t.Fatalf("got %d stanzas, want 3 sections:\n%s", len(got), dump(got))
	}
	if got[0].Lines[0] != "[defaults]" {
		t.Errorf("stanza 0 = %q, want [defaults]", got[0].Lines[0])
	}
	if got[2].Lines[0] != "[Peer]" {
		t.Errorf("stanza 2 = %q, want [Peer]", got[2].Lines[0])
	}
	if !strings.Contains(got[2].RawText, "2001:db8::1/128") {
		t.Errorf("IPv6 AllowedIPs line was split off:\n%s", dump(got))
	}
}

// TestHeaderBlankBetweenDevicesIsNotAStanza is the regression gate for a bug
// found against real `lspci -vv` output: the blank line that closes a device
// was itself being emitted as a one-line blank stanza, inflating the count.
func TestHeaderBlankBetweenDevicesIsNotAStanza(t *testing.T) {
	const input = "00:00.0 Alpha\n\tone\n\tCapabilities: [e0] x\n\n00:00.2 Beta\n\ttwo\n"
	sc := newScanner(t, input, ModeHeader)
	got := collect(t, sc)

	if len(got) != 2 {
		t.Fatalf("got %d stanzas, want 2 (no blank stanza):\n%s", len(got), dump(got))
	}
	for i, s := range got {
		if len(s.Lines) == 0 || strings.TrimSpace(s.Lines[0]) == "" {
			t.Errorf("stanza %d begins with a blank line: %v", i, s.Lines)
		}
	}
	if got[1].Lines[0] != "00:00.2 Beta" {
		t.Errorf("second stanza = %q, want the 00:00.2 header", got[1].Lines[0])
	}
}

// TestHeaderPreservesAllInputLinesExceptSeparators verifies the scanner is
// lossless: every non-blank input line appears exactly once, in order, across
// the emitted stanzas.
func TestHeaderPreservesAllInputLinesExceptSeparators(t *testing.T) {
	sc := newScanner(t, mockLspci, ModeHeader)
	got := collect(t, sc)

	var flattened []string
	for _, s := range got {
		flattened = append(flattened, s.Lines...)
	}

	var want []string
	for _, line := range strings.Split(mockLspci, "\n") {
		if strings.TrimSpace(line) != "" {
			want = append(want, line)
		}
	}

	if len(flattened) != len(want) {
		t.Fatalf("got %d lines across stanzas, want %d:\n%s", len(flattened), len(want), dump(got))
	}
	for i := range want {
		if flattened[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, flattened[i], want[i])
		}
	}
}

// TestHeaderTrailingBlankNotAttached verifies blank lines at end of input do
// not linger on the final stanza or become their own.
func TestHeaderTrailingBlankNotAttached(t *testing.T) {
	sc := newScanner(t, "00:00.0 Alpha\n\tone\n\n\n", ModeHeader)
	got := collect(t, sc)

	if len(got) != 1 {
		t.Fatalf("got %d stanzas, want 1", len(got))
	}
	if last := got[0].Lines[len(got[0].Lines)-1]; last == "" {
		t.Errorf("final stanza kept a trailing blank line: %v", got[0].Lines)
	}
}

// --- Mode C: Indentation Ridge --------------------------------------------

// mockYAML is a nested config resembling a values file.
const mockYAML = `server:
  host: 0.0.0.0
  port: 8080
  tls:
    enabled: true
    cert: /etc/ssl/cert.pem
logging:
  level: info
  sinks:
    - stdout
    - file
`

// TestIndentGroupsNestedChildren is the Mode C gate: each top-level key becomes
// a stanza that owns all of its more-indented children.
func TestIndentGroupsNestedChildren(t *testing.T) {
	sc := newScanner(t, mockYAML, ModeIndent)
	got := collect(t, sc)

	if len(got) != 2 {
		t.Fatalf("got %d stanzas, want 2 top-level keys:\n%s", len(got), dump(got))
	}

	if got[0].Lines[0] != "server:" {
		t.Errorf("stanza 0 = %q, want server:", got[0].Lines[0])
	}
	if len(got[0].Lines) != 6 {
		t.Errorf("server stanza has %d lines, want 6:\n%s", len(got[0].Lines), got[0].RawText)
	}
	if !strings.Contains(got[0].RawText, "cert: /etc/ssl/cert.pem") {
		t.Errorf("nested tls block was split off:\n%s", got[0].RawText)
	}

	if got[1].Lines[0] != "logging:" {
		t.Errorf("stanza 1 = %q, want logging:", got[1].Lines[0])
	}
	if len(got[1].Lines) != 5 {
		t.Errorf("logging stanza has %d lines, want 5:\n%s", len(got[1].Lines), got[1].RawText)
	}
	for i := range got {
		if got[i].BoundaryType != BoundaryIndent {
			t.Errorf("stanza %d BoundaryType = %q, want %q", i, got[i].BoundaryType, BoundaryIndent)
		}
	}
}

// TestIndentClosesOnDedent verifies a return to the anchor level starts a new
// stanza.
func TestIndentClosesOnDedent(t *testing.T) {
	const input = "root:\n  child: 1\nnext:\n  child: 2\n"
	sc := newScanner(t, input, ModeIndent)
	got := collect(t, sc)

	if len(got) != 2 {
		t.Fatalf("got %d stanzas, want 2:\n%s", len(got), dump(got))
	}
	if got[1].Lines[0] != "next:" {
		t.Errorf("stanza 1 = %q, want next:", got[1].Lines[0])
	}
}

// TestIndentIntermediateDedentStartsStanza verifies a partial dedent (not all
// the way to zero) also starts a new stanza, since the ridge moved.
func TestIndentIntermediateDedentStartsStanza(t *testing.T) {
	const input = "a:\n  b: 1\n  c: 2\nx:\n  y:\n    z: 3\n"
	sc := newScanner(t, input, ModeIndent)
	got := collect(t, sc)

	if len(got) != 2 {
		t.Fatalf("got %d stanzas, want 2:\n%s", len(got), dump(got))
	}
	if len(got[0].Lines) != 3 {
		t.Errorf("a stanza has %d lines, want 3: %v", len(got[0].Lines), got[0].Lines)
	}
	if len(got[1].Lines) != 3 {
		t.Errorf("x stanza has %d lines, want 3: %v", len(got[1].Lines), got[1].Lines)
	}
}

// TestIndentEmptyInput verifies clean EOF.
func TestIndentEmptyInput(t *testing.T) {
	sc := newScanner(t, "", ModeIndent)
	if got := collect(t, sc); len(got) != 0 {
		t.Fatalf("got %d stanzas, want 0", len(got))
	}
}

// --- Header anchor detection ----------------------------------------------

// TestHeaderAnchorFalsePositives guards the patterns that motivated the
// BDF-domain requirement: timestamps and bare hex pairs must not anchor.
func TestHeaderAnchorFalsePositives(t *testing.T) {
	notHeaders := []string{
		"2024-01-01 12:00:00 ERROR something failed",
		"ab:cd",
		"12:34",
		"de:ad:be:ef",
		"Version 1.2:3 is available",
		"[not closed",
		"  leading indented text",
		"",
	}
	for _, line := range notHeaders {
		if isHeaderLine(line) {
			t.Errorf("isHeaderLine(%q) = true, want false", line)
		}
	}

	headers := []string{
		"[defaults]",
		"[Interface]",
		"00:00.0 Host bridge: Intel Corporation",
		"0000:06:00.0 Ethernet controller",
		"41:00.0 VGA compatible controller",
		"0000:41:00.0",
		"/var/log/syslog:42: something happened",
	}
	for _, line := range headers {
		if !isHeaderLine(line) {
			t.Errorf("isHeaderLine(%q) = false, want true", line)
		}
	}
}

// TestBDFFunctionNibbleIsValidated verifies the device.function nibble must be
// 0-7, which is what separates a real BDF from an arbitrary hex pair.
func TestBDFFunctionNibbleIsValidated(t *testing.T) {
	if !isHeaderLine("00:1f.6 Ethernet controller") {
		t.Error("valid function nibble .6 rejected")
	}
	if isHeaderLine("00:1f.9 bogus") {
		t.Error("invalid function nibble .9 accepted")
	}
}

// --- Auto-detection -------------------------------------------------------

// TestDetectMode is the auto-detection gate.
func TestDetectMode(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"lspci", mockLspci, ModeHeader},
		// Real lspci -vv output is only ~2.5% header lines, because each device
		// contributes one header and dozens of indented capability lines. An
		// earlier threshold of 10% misclassified it as indent mode. This fixture
		// reproduces that density.
		{"lspci-sparse-headers", mockLspciVV, ModeHeader},
		{"yaml", mockYAML, ModeIndent},
		{"ini", "[a]\nx = 1\n[b]\ny = 2\n", ModeHeader},
		{"prose", "the quick brown fox\njumped over the lazy dog\nnothing to see here\n", ModeStream},
		{"empty", "", ModeStream},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc, err := NewVisualScanner(strings.NewReader(tc.input), ModeAuto, 0)
			if err != nil {
				t.Fatalf("NewVisualScanner: %v", err)
			}
			if got := sc.Mode(); got != tc.want {
				t.Errorf("detected mode = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAutoSampleSizeIsRespected verifies the sample limit is honored: a stream
// whose first lines look like prose but whose later lines are headers must be
// classified from the head alone, and --sample 200 must change the answer.
func TestAutoSampleSizeIsRespected(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("plain prose line\n")
	}
	for i := 0; i < 20; i++ {
		b.WriteString("00:00.0 Device header\n\tFlags: fast devsel\n")
	}
	input := b.String()

	small, err := NewVisualScanner(strings.NewReader(input), ModeAuto, 5)
	if err != nil {
		t.Fatalf("NewVisualScanner: %v", err)
	}
	if small.Mode() != ModeStream {
		t.Errorf("with sample 5, mode = %q, want %q", small.Mode(), ModeStream)
	}

	large, err := NewVisualScanner(strings.NewReader(input), ModeAuto, 100)
	if err != nil {
		t.Fatalf("NewVisualScanner: %v", err)
	}
	if large.Mode() != ModeHeader {
		t.Errorf("with sample 100, mode = %q, want %q", large.Mode(), ModeHeader)
	}
}

// --- Construction and robustness ------------------------------------------

// TestNewVisualScannerRejectsUnknownMode verifies a typo is a hard error rather
// than a silent fallback.
func TestNewVisualScannerRejectsUnknownMode(t *testing.T) {
	if _, err := NewVisualScanner(strings.NewReader("x\n"), "bogus", 0); err == nil {
		t.Fatal("NewVisualScanner accepted mode \"bogus\", want error")
	}
}

// TestModeAliasAndCase verifies mode strings are normalized.
func TestModeAliasAndCase(t *testing.T) {
	for _, in := range []string{"HEADER", "Header", "ini", " header "} {
		sc, err := NewVisualScanner(strings.NewReader("x\n"), in, 0)
		if err != nil {
			t.Fatalf("NewVisualScanner(%q): %v", in, err)
		}
		if sc.Mode() != ModeHeader {
			t.Errorf("mode %q resolved to %q, want %q", in, sc.Mode(), ModeHeader)
		}
	}
}

// TestScannerStripsANSIColors verifies that colorized output still anchors,
// since `lspci -v | colordiff` is a common real-world input.
func TestScannerStripsANSIColors(t *testing.T) {
	const input = "\x1b[01;34m00:00.0\x1b[0m Host bridge: Intel\n\tFlags: fast devsel\n"
	sc := newScanner(t, input, ModeHeader)
	got := collect(t, sc)

	if len(got) != 1 {
		t.Fatalf("got %d stanzas, want 1:\n%s", len(got), dump(got))
	}
	if strings.Contains(got[0].RawText, "\x1b") {
		t.Errorf("ANSI escapes survived into stanza text: %q", got[0].RawText)
	}
	if !strings.HasPrefix(got[0].Lines[0], "00:00.0") {
		t.Errorf("stanza = %q, want it to start with the BDF", got[0].Lines[0])
	}
}

// TestVisualScannerHandlesLongLines verifies the raised line limit applies here
// too.
func TestVisualScannerHandlesLongLines(t *testing.T) {
	long := strings.Repeat("x", 512*1024)
	sc := newScanner(t, long+"\n", ModeStream)
	got := collect(t, sc)

	if len(got) != 1 {
		t.Fatalf("got %d stanzas, want 1", len(got))
	}
	if len(got[0].Lines[0]) != len(long) {
		t.Errorf("line length = %d, want %d", len(got[0].Lines[0]), len(long))
	}
}

// TestScannerInputWithoutTrailingNewline verifies the final unterminated line
// is still emitted.
func TestScannerInputWithoutTrailingNewline(t *testing.T) {
	sc := newScanner(t, "a\nb", ModeStream)
	got := collect(t, sc)

	if len(got) != 2 {
		t.Fatalf("got %d stanzas, want 2", len(got))
	}
	if got[1].Lines[0] != "b" {
		t.Errorf("last stanza = %v, want [b]", got[1].Lines)
	}
}

// dump renders stanzas for readable failure output.
func dump(stanzas []*Stanza) string {
	var b strings.Builder
	for i, s := range stanzas {
		b.WriteString("stanza ")
		b.WriteString(string(rune('0' + i)))
		b.WriteString(" (")
		b.WriteString(s.BoundaryType)
		b.WriteString("):\n")
		for _, l := range s.Lines {
			b.WriteString("  |")
			b.WriteString(l)
			b.WriteString("|\n")
		}
	}
	return b.String()
}

// Verbatim `lspci -vv` stanzas from a real machine. These matter because the
// indentation is a tab rather than spaces, and the LnkSta line carries a
// "(downgraded)" parenthetical before its Width field.
const (
	realNVMeStanza = "04:00.0 Non-Volatile memory controller: Samsung Electronics Co Ltd NVMe SSD Controller PM9A1\n" +
		"\tLnkCap:\tPort #0, Speed 32GT/s, Width x4, ASPM L1, Exit Latency L1 <64us\n" +
		"\tLnkSta:\tSpeed 2.5GT/s (downgraded), Width x4\n" +
		"\tKernel driver in use: nvme\n"

	realRootPortStanza = "00:01.2 PCI bridge: Advanced Micro Devices, Inc. [AMD]\n" +
		"\tLnkCap:\tPort #0, Speed 32GT/s, Width x4, ASPM not supported\n" +
		"\tLnkSta:\tSpeed 2.5GT/s, Width x4\n" +
		"\tKernel driver in use: pcieport\n"

	realHealthyStanza = "00:01.1 PCI bridge: Advanced Micro Devices, Inc. [AMD]\n" +
		"\tLnkCap:\tPort #0, Speed 16GT/s, Width x16, ASPM L1\n" +
		"\tLnkSta:\tSpeed 16GT/s, Width x16\n" +
		"\tKernel driver in use: pcieport\n"
)

// TestChunkRealLspciStanzas verifies the header heuristic against realistic
// tab-indented device blocks taken from a real `lspci -vv`, where indentation is
// a tab and each device spans several lines.
func TestChunkRealLspciStanzas(t *testing.T) {
	input := realNVMeStanza + "\n" + realRootPortStanza + "\n" + realHealthyStanza

	stanzas, err := chunkLines(strings.Split(input, "\n"), ModeHeader, 0)
	if err != nil {
		t.Fatalf("chunkLines: %v", err)
	}
	if len(stanzas) != 3 {
		t.Fatalf("got %d stanzas, want 3 devices:\n%s", len(stanzas), dump(stanzas))
	}
	for i, want := range []string{"04:00.0", "00:01.2", "00:01.1"} {
		if !strings.HasPrefix(stanzas[i].Lines[0], want) {
			t.Errorf("stanza %d starts with %q, want prefix %q", i, stanzas[i].Lines[0], want)
		}
		// Every continuation line must stay attached to its device.
		if len(stanzas[i].Lines) != 4 {
			t.Errorf("stanza %d has %d lines, want 4", i, len(stanzas[i].Lines))
		}
	}
}

// TestRealLspciTabIndentationBeatsSpaceExpectation verifies a tab-indented
// continuation is recognised as indented, since leadingSpaces treats a tab as
// one column.
func TestRealLspciTabIndentationBeatsSpaceExpectation(t *testing.T) {
	if got := leadingSpaces("\tLnkSta:\tSpeed 2.5GT/s"); got != 1 {
		t.Errorf("leadingSpaces(tab-indented) = %d, want 1", got)
	}
	if isHeaderLine("\tLnkSta:\tSpeed 2.5GT/s") {
		t.Error("an indented LnkSta line was treated as a device header")
	}
}
