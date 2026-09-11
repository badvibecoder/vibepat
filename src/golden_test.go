package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vibepat/src/registry"
)

// goldenLspci is real resolved-root `lspci -vv` output captured from the
// development machine. Unlike the small hand-written fixtures in the other test
// files, it carries the true shape of this format: tab indentation, a very low
// header-to-line ratio, and link capability/status pairs on separate lines.
const goldenLspci = "testdata/lspci.txt"

// goldenDeviceCount is the number of PCI devices in the fixture.
const goldenDeviceCount = 36

// goldenDegradedBDFs are the devices whose negotiated PCIe link is below their
// advertised capability. This was cross-checked independently against the
// machine's sysfs `current_link_speed` / `max_link_speed` attributes, which
// agreed exactly.
var goldenDegradedBDFs = []string{
	"00:01.2",
	"00:02.1",
	"00:03.2",
	"04:00.0",
	"05:00.0",
}

// readGolden loads the fixture, failing clearly if it is missing.
func readGolden(t *testing.T) []string {
	t.Helper()

	data, err := os.ReadFile(goldenLspci)
	if err != nil {
		t.Fatalf("read %s: %v", goldenLspci, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// TestGoldenLspciAutoDetectsHeaderMode verifies the auto-detection threshold
// against real input. The fixture is only ~2.5% header lines, which the original
// 10% threshold misclassified as indent mode.
func TestGoldenLspciAutoDetectsHeaderMode(t *testing.T) {
	data, err := os.ReadFile(goldenLspci)
	if err != nil {
		t.Fatalf("read %s: %v", goldenLspci, err)
	}

	sc, err := NewVisualScanner(strings.NewReader(string(data)), ModeAuto, 0)
	if err != nil {
		t.Fatalf("NewVisualScanner: %v", err)
	}
	if got := sc.Mode(); got != ModeHeader {
		t.Errorf("detected %q, want %q for real lspci -vv output", got, ModeHeader)
	}
}

// TestGoldenLspciChunkingIsLossless is the strongest property this tool offers
// on real input: every non-blank line appears exactly once, in order, across the
// emitted stanzas. A chunker that silently drops lines would be worse than
// useless on a config or log.
func TestGoldenLspciChunkingIsLossless(t *testing.T) {
	lines := readGolden(t)

	stanzas, err := chunkLines(lines, ModeHeader, 0)
	if err != nil {
		t.Fatalf("chunkLines: %v", err)
	}
	if len(stanzas) != goldenDeviceCount {
		t.Fatalf("got %d stanzas, want %d devices", len(stanzas), goldenDeviceCount)
	}

	var flattened []string
	for _, s := range stanzas {
		flattened = append(flattened, s.Lines...)

		if s.BoundaryType != BoundaryHeader {
			t.Errorf("stanza %q has boundary %q, want %q",
				firstLine(s), s.BoundaryType, BoundaryHeader)
		}
	}

	var want []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			want = append(want, l)
		}
	}

	if len(flattened) != len(want) {
		t.Fatalf("stanzas hold %d lines, input has %d non-blank lines", len(flattened), len(want))
	}
	for i := range want {
		if flattened[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, flattened[i], want[i])
		}
	}
}

// TestGoldenLspciEveryStanzaStartsWithADevice verifies grouping against real
// input: each stanza begins with a BDF header and holds only indented
// continuations after it.
func TestGoldenLspciEveryStanzaStartsWithADevice(t *testing.T) {
	lines := readGolden(t)

	stanzas, err := chunkLines(lines, ModeHeader, 0)
	if err != nil {
		t.Fatalf("chunkLines: %v", err)
	}

	for i, s := range stanzas {
		if leadingSpaces(s.Lines[0]) != 0 {
			t.Errorf("stanza %d begins indented: %q", i, s.Lines[0])
		}
		if !registry.IsRegistered(registry.TokenBDF) {
			t.Fatal("bdf token is not registered")
		}
		m, _ := registry.Lookup(registry.TokenBDF)
		if got := m.Match(s.Lines[0]); len(got) == 0 {
			t.Errorf("stanza %d does not begin with a BDF: %q", i, s.Lines[0])
		}

		for _, l := range s.Lines[1:] {
			if strings.TrimSpace(l) == "" {
				continue
			}
			if leadingSpaces(l) == 0 {
				t.Errorf("stanza %d contains an unindented continuation: %q", i, l)
			}
		}
	}
}

// TestGoldenLspciLinkDowngradeMatchesSysfs cross-checks the token against an
// independently obtained source: the machine's sysfs link attributes, which
// reported the same five devices.
func TestGoldenLspciLinkDowngradeMatchesSysfs(t *testing.T) {
	lines := readGolden(t)

	stanzas, err := chunkLines(lines, ModeHeader, 0)
	if err != nil {
		t.Fatalf("chunkLines: %v", err)
	}

	q, err := Parse("get all [bdf, link_downgrade] require all")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	results, err := SearchStanzas(q, stanzas, lineIndex(lines, stanzas))
	if err != nil {
		t.Fatalf("SearchStanzas: %v", err)
	}

	var got []string
	for _, r := range results {
		if bdfs := r.Matched[registry.TokenBDF]; len(bdfs) > 0 {
			got = append(got, bdfs[0])
		}
	}

	if len(got) != len(goldenDegradedBDFs) {
		t.Fatalf("flagged %v, want %v", got, goldenDegradedBDFs)
	}
	for i := range goldenDegradedBDFs {
		if got[i] != goldenDegradedBDFs[i] {
			t.Fatalf("flagged %v, want %v", got, goldenDegradedBDFs)
		}
	}
}

// TestGoldenLspciHealthyLinksAreNotFlagged is the false-positive gate on real
// data: 31 of the 36 devices have a healthy link and must stay silent.
func TestGoldenLspciHealthyLinksAreNotFlagged(t *testing.T) {
	lines := readGolden(t)

	stanzas, err := chunkLines(lines, ModeHeader, 0)
	if err != nil {
		t.Fatalf("chunkLines: %v", err)
	}

	// Devices with no link capability data cannot be judged, so only count the
	// ones that actually report both LnkCap and LnkSta.
	m, _ := registry.Lookup(registry.TokenLinkDowngrade)

	var judged, flagged int
	for _, s := range stanzas {
		if !strings.Contains(s.RawText, "LnkCap") || !strings.Contains(s.RawText, "LnkSta") {
			continue
		}
		judged++
		if len(m.Match(s.RawText)) > 0 {
			flagged++
		}
	}

	if judged == 0 {
		t.Fatal("fixture contains no link capability data; the token is untested here")
	}
	if flagged != len(goldenDegradedBDFs) {
		t.Errorf("flagged %d of %d links with data, want %d",
			flagged, judged, len(goldenDegradedBDFs))
	}
}

// TestGoldenLspciReadOnlyQueryNeverWrites verifies a query against the fixture
// leaves it untouched.
func TestGoldenLspciReadOnlyQueryNeverWrites(t *testing.T) {
	before, err := os.ReadFile(goldenLspci)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var out, diag bytes.Buffer
	// The grammar is now given as raw positional arguments.
	args := []string{"--mode", "header", "get", "all", "[bdf]", goldenLspci}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	after, err := os.ReadFile(goldenLspci)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a read-only query modified the fixture")
	}
	if _, err := os.Stat(goldenLspci + BackupSuffix); !os.IsNotExist(err) {
		t.Error("a read-only query created a backup")
	}
}

// TestGoldenLspciFixtureIsPresent guards against the fixture being deleted,
// which would silently turn the tests above into no-ops.
func TestGoldenLspciFixtureIsPresent(t *testing.T) {
	info, err := os.Stat(filepath.Clean(goldenLspci))
	if err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("fixture is empty")
	}

	lines := readGolden(t)
	if len(lines) < 100 {
		t.Errorf("fixture has %d lines, expected real lspci output", len(lines))
	}
}

// firstLine returns the first line of a stanza, or "" for an empty one.
func firstLine(s *Stanza) string {
	if s == nil || len(s.Lines) == 0 {
		return ""
	}
	return s.Lines[0]
}
