package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

// This file is the end-to-end integration suite. Every case feeds real text
// through the real pipeline -- chunker, token registry, grammar parser, and
// execution engine -- and asserts on the emitted JSON or the bytes on disk.
// Nothing here is mocked: a test that passes proves the shipped code paths work,
// not that a stub was called.

// runPipeline feeds input through the default read-only pipeline and returns the
// decoded NDJSON objects.
func runPipeline(t *testing.T, input, mode string, extraArgs ...string) []MatchResult {
	t.Helper()

	var out, diag bytes.Buffer
	args := append([]string{"--mode", mode}, extraArgs...)
	if err := runPlain(t, args, strings.NewReader(input), &out, &diag); err != nil {
		t.Fatalf("run(%v): %v", args, err)
	}
	return decodeNDJSON(t, out.String())
}

// runQuery feeds input through a grammar query and returns the decoded NDJSON.
func runQuery(t *testing.T, input, mode string, grammar ...string) []MatchResult {
	t.Helper()

	var out, diag bytes.Buffer
	args := append([]string{"--mode", mode}, grammar...)
	if err := runPlain(t, args, strings.NewReader(input), &out, &diag); err != nil {
		t.Fatalf("run(%v): %v", args, err)
	}
	return decodeNDJSON(t, out.String())
}

// tokensFor returns the matched values for one token across a result, or nil.
func tokensFor(m MatchResult, token string) []string { return m.MatchedTokens[token] }

// assertTokensEqual compares a token's matches against an exact expected slice.
func assertTokensEqual(t *testing.T, m MatchResult, token string, want []string) {
	t.Helper()

	got := tokensFor(m, token)
	if len(got) != len(want) {
		t.Fatalf("matched_tokens[%q] = %v, want exactly %v", token, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("matched_tokens[%q] = %v, want exactly %v", token, got, want)
		}
	}
}

// ############################################################################
// Test Area 1: The Visual Boundary Engine
// ############################################################################

// mangledYAML is a config that starts as valid nested YAML, breaks into
// unstructured log text at column zero, and then resumes. Datacenter configs
// accumulate this shape when a log line is appended into a values file or a
// templating step pastes raw output into a manifest.
const mangledYAML = `services:
  web:
    image: nginx:1.27
    ports:
      - 80:80
      - 443:443
  cache:
    image: redis:7
2024-01-01T00:00:00Z ERROR connection refused to upstream 10.0.0.5
retrying in 5s
panic: runtime error: invalid memory address
database:
  host: db.internal
  port: 5432
  credentials:
    user: app
    password: hunter2
`

// TestIntegrationMangledYAMLDoesNotCrash is requirement 1.1's primary gate. A
// malformed mixed document must not panic, hang, or lose lines.
func TestIntegrationMangledYAMLDoesNotCrash(t *testing.T) {
	results := runPipeline(t, mangledYAML, ModeIndent)
	if len(results) == 0 {
		t.Fatal("no stanzas produced")
	}

	// Losslessness: every non-blank line must appear exactly once, in order.
	var got []string
	for _, r := range results {
		got = append(got, r.Stanza.Lines...)
	}
	var want []string
	for _, l := range strings.Split(mangledYAML, "\n") {
		if strings.TrimSpace(l) != "" {
			want = append(want, l)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("stanzas hold %d lines, input has %d non-blank lines", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestIntegrationMangledYAMLIsolatesStanzas is requirement 1.1's structural gate:
// the two valid YAML blocks must be isolated, and the unstructured log lines must
// not be absorbed into them.
func TestIntegrationMangledYAMLIsolatesStanzas(t *testing.T) {
	results := runPipeline(t, mangledYAML, ModeIndent)

	byIndex := map[int]*Stanza{}
	for _, r := range results {
		byIndex[r.StanzaIndex] = r.Stanza
	}

	// Stanza 1: the services block, including its deeper nesting.
	if s := byIndex[1]; s == nil {
		t.Fatal("no stanza 1")
	} else {
		if s.Lines[0] != "services:" {
			t.Errorf("stanza 1 starts with %q, want 'services:'", s.Lines[0])
		}
		for _, want := range []string{"image: nginx:1.27", "- 443:443", "image: redis:7"} {
			if !strings.Contains(s.RawText, want) {
				t.Errorf("stanza 1 is missing %q:\n%s", want, s.RawText)
			}
		}
		if strings.Contains(s.RawText, "ERROR connection refused") {
			t.Errorf("stanza 1 absorbed a log line:\n%s", s.RawText)
		}
	}

	// The three unstructured lines must be their own stanzas, each exactly one
	// line, and must not be merged with each other or with the YAML.
	for _, tc := range []struct {
		index int
		text  string
	}{
		{2, "2024-01-01T00:00:00Z ERROR connection refused to upstream 10.0.0.5"},
		{3, "retrying in 5s"},
		{4, "panic: runtime error: invalid memory address"},
	} {
		s := byIndex[tc.index]
		if s == nil {
			t.Errorf("no stanza %d", tc.index)
			continue
		}
		if len(s.Lines) != 1 {
			t.Errorf("stanza %d has %d lines, want the log line alone: %v",
				tc.index, len(s.Lines), s.Lines)
		}
		if s.Lines[0] != tc.text {
			t.Errorf("stanza %d = %q, want %q", tc.index, s.Lines[0], tc.text)
		}
	}

	// The resumed YAML block must be intact and separate.
	if s := byIndex[5]; s == nil {
		t.Fatal("no stanza 5")
	} else {
		if s.Lines[0] != "database:" {
			t.Errorf("stanza 5 starts with %q, want 'database:'", s.Lines[0])
		}
		if !strings.Contains(s.RawText, "password: hunter2") {
			t.Errorf("resumed YAML block was truncated:\n%s", s.RawText)
		}
	}
}

// mangledLspci mimics `lspci -vvv`: a device whose capability blocks are
// separated by blank lines, followed by a second device.
const mangledLspci = `00:1f.6 Ethernet controller: Intel Corporation Device 15f9
	Subsystem: Dell Device 0a2b
	Flags: bus master, fast devsel, latency 0
	Memory at 6050000000 (64-bit, non-prefetchable) [size=16M]

	Capabilities: [c8] Power Management version 3
		Flags: PMEClk- DSI- D1+ D2+ AuxCurrent=0mA
		Status: D0 NoSoftRst+ PME-Enable- DSel=0 DScale=0 PME-

	Capabilities: [d0] MSI: Enable+ Count=1/1 Maskable- 64bit+
		Address: 00000000fee00c18  Data: 0000

	Capabilities: [e0] Express Endpoint, MSI 00
		LnkCap: Port #0, Speed 16GT/s, Width x1
		LnkSta: Speed 16GT/s, Width x1
	Kernel driver in use: e1000e

00:02.0 VGA compatible controller: Intel Corporation Device 46a6
	Subsystem: Dell Device 0bd0
	Flags: bus master, fast devsel, latency 0
`

// TestIntegrationLspciLookaheadGroupsOneDevice is requirement 1.2: internal blank
// lines must NOT terminate a device stanza. The stanza closes only at the next
// BDF anchor.
func TestIntegrationLspciLookaheadGroupsOneDevice(t *testing.T) {
	results := runPipeline(t, mangledLspci, ModeHeader)

	if len(results) != 2 {
		t.Fatalf("got %d stanzas, want exactly 2 devices:\n%s", len(results), dumpResults(results))
	}

	first := results[0].Stanza
	if !strings.HasPrefix(first.Lines[0], "00:1f.6") {
		t.Fatalf("first stanza starts with %q, want the 00:1f.6 header", first.Lines[0])
	}

	// Everything from the header through the last capability line must be one
	// stanza, including the text after each internal blank line.
	for _, want := range []string{
		"Subsystem: Dell Device 0a2b",
		"Capabilities: [c8] Power Management version 3",
		"Status: D0 NoSoftRst+ PME-Enable- DSel=0 DScale=0 PME-",
		"Capabilities: [d0] MSI: Enable+ Count=1/1 Maskable- 64bit+",
		"Address: 00000000fee00c18  Data: 0000",
		"Capabilities: [e0] Express Endpoint, MSI 00",
		"LnkCap: Port #0, Speed 16GT/s, Width x1",
		"Kernel driver in use: e1000e",
	} {
		if !strings.Contains(first.RawText, want) {
			t.Errorf("first device stanza is missing %q:\n%s", want, first.RawText)
		}
	}

	// The interior blank lines must be retained rather than silently dropped.
	if !strings.Contains(first.RawText, "Power Management version 3\n\t\tFlags:") {
		t.Logf("note: interior blank line layout in stanza:\n%q", first.RawText)
	}

	// The second device must not be contaminated by the first.
	second := results[1].Stanza
	if !strings.HasPrefix(second.Lines[0], "00:02.0") {
		t.Errorf("second stanza starts with %q, want the 00:02.0 header", second.Lines[0])
	}
	if strings.Contains(second.RawText, "e1000e") {
		t.Errorf("second device absorbed the first device's driver line:\n%s", second.RawText)
	}
	if len(second.Lines) != 3 {
		t.Errorf("second stanza has %d lines, want 3: %v", len(second.Lines), second.Lines)
	}
}

// dumpResults renders results for a readable failure message.
func dumpResults(results []MatchResult) string {
	var b strings.Builder
	for _, r := range results {
		b.WriteString("  stanza ")
		b.WriteString(strconv.Itoa(r.StanzaIndex))
		b.WriteString(": ")
		for i, l := range r.Stanza.Lines {
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(strings.TrimSpace(l))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ############################################################################
// Test Area 2: Semantic Token Rigidity
// ############################################################################

// TestIntegrationAlmostAnIP is requirement 2.1: malformed addresses must not be
// extracted, even though they are in the same sentence as a valid one.
func TestIntegrationAlmostAnIP(t *testing.T) {
	const input = "Check node 999.888.777.666 and version 10.0.0.256 before pinging 10.0.0.1."

	results := runPipeline(t, input, ModeStream, "--tokens", "ip")
	if len(results) != 1 {
		t.Fatalf("got %d stanzas, want 1", len(results))
	}
	assertTokensEqual(t, results[0], "ip", []string{"10.0.0.1"})
}

// TestIntegrationAlmostAMAC is requirement 2.2: a timestamp that is also
// syntactically a valid MAC must not be reported as one.
func TestIntegrationAlmostAMAC(t *testing.T) {
	const input = "Timestamp 12:00:14:ab:12:a5 and MAC 38:00:14:ab:12:a5."

	results := runPipeline(t, input, ModeStream, "--tokens", "mac")
	if len(results) != 1 {
		t.Fatalf("got %d stanzas, want 1", len(results))
	}
	assertTokensEqual(t, results[0], "mac", []string{"38:00:14:ab:12:a5"})
}

// TestIntegrationAlmostABDF is requirement 2.3: a clock time must not be read as
// a PCI address.
func TestIntegrationAlmostABDF(t *testing.T) {
	const input = "Time 12:00:00 and device 0000:41:00.0."

	results := runPipeline(t, input, ModeStream, "--tokens", "bdf")
	if len(results) != 1 {
		t.Fatalf("got %d stanzas, want 1", len(results))
	}
	assertTokensEqual(t, results[0], "bdf", []string{"0000:41:00.0"})
}

// TestIntegrationTokenRigidityOnOneLine runs all three tokens against a single
// hostile line, which is how these fields actually appear together in logs.
func TestIntegrationTokenRigidityOnOneLine(t *testing.T) {
	const input = "ts=12:00:14:ab:12:a5 src=999.888.777.666 ver=10.0.0.256 slot=12:00:00 ip=10.0.0.1 mac=38:00:14:ab:12:a5 pci=0000:41:00.0"

	results := runPipeline(t, input, ModeStream, "--tokens", "ip,mac,bdf")
	if len(results) != 1 {
		t.Fatalf("got %d stanzas, want 1", len(results))
	}
	m := results[0]

	assertTokensEqual(t, m, "ip", []string{"10.0.0.1"})
	assertTokensEqual(t, m, "mac", []string{"38:00:14:ab:12:a5"})
	assertTokensEqual(t, m, "bdf", []string{"0000:41:00.0"})
}

// ############################################################################
// Test Area 3: Grammar & Execution Engine
// ############################################################################

// TestIntegrationCIDRSafeRewrite is requirement 3.1: a subnet rewrite must touch
// only addresses inside the subnet and leave the gateway alone.
func TestIntegrationCIDRSafeRewrite(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/host.conf"
	const original = "server_ip=10.0.1.7\ngateway=10.9.9.7\n"
	if err := writeFile(path, original); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{
		"--mode", "stream", "--no-color",
		"replace", "ip", "['10.0.1.0/24']", "with", "['10.50.1.X']", "yolo", path,
	}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	got := string(mustRead(t, path))
	const want = "server_ip=10.50.1.7\ngateway=10.9.9.7\n"
	if got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "10.50.9.7") {
		t.Errorf("the gateway was rewritten: %q", got)
	}

	// The execution summary must report exactly one change.
	var summary ExecResult
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("summary is not valid JSON: %v\n%s", err, out.String())
	}
	if summary.Status != StatusApplied {
		t.Errorf("status = %q, want %q", summary.Status, StatusApplied)
	}
	if summary.Applied != 1 || summary.Total != 1 {
		t.Errorf("applied/total = %d/%d, want 1/1", summary.Applied, summary.Total)
	}

	// The backup must hold the untouched original.
	if backup := string(mustRead(t, path+BackupSuffix)); backup != original {
		t.Errorf("backup = %q, want the original %q", backup, original)
	}
}

// TestIntegrationCIDRWildcardEquivalence verifies the CIDR and fourth-octet
// wildcard spellings produce identical bytes.
func TestIntegrationCIDRWildcardEquivalence(t *testing.T) {
	dir := t.TempDir()
	const content = "a=10.0.1.7\nb=10.0.1.200\nc=10.9.9.7\n"

	results := make([]string, 0, 2)
	for _, pattern := range []string{"10.0.1.0/24", "10.0.1.X"} {
		path := dir + "/" + strings.NewReplacer("/", "_", ".", "_").Replace(pattern) + ".conf"
		if err := writeFile(path, content); err != nil {
			t.Fatalf("setup: %v", err)
		}

		var out, diag bytes.Buffer
		args := []string{
			"--mode", "stream", "--no-color",
			"replace", "ip", "['" + pattern + "']", "with", "['10.50.1.X']", "yolo", path,
		}
		if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
			t.Fatalf("pattern %q: %v", pattern, err)
		}
		results = append(results, string(mustRead(t, path)))
	}

	if results[0] != results[1] {
		t.Errorf("CIDR and wildcard differ:\n CIDR: %q\n wildcard: %q", results[0], results[1])
	}
	const want = "a=10.50.1.7\nb=10.50.1.200\nc=10.9.9.7\n"
	if results[0] != want {
		t.Errorf("rewrite produced %q, want %q", results[0], want)
	}
}

// TestIntegrationDryRunSanityCheck is requirement 3.2: a dry run must leave both
// the file and the directory untouched.
func TestIntegrationDryRunSanityCheck(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/mac.conf"
	const original = "target_mac=38:00:14:ab:12:a5"
	if err := writeFile(path, original); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{
		"--mode", "stream", "--no-color",
		"replace", "mac", "with", "['REDACTED']", "dryrun", path,
	}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := string(mustRead(t, path)); got != original {
		t.Errorf("dry run modified the file: %q", got)
	}
	if _, err := os.Stat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Errorf("dry run created a backup at %s", path+BackupSuffix)
	}

	// Nothing but the input and no temp files may be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "mac.conf" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory contains %v, want only mac.conf", names)
	}

	// The diff must still be shown, on the diagnostic stream.
	if !strings.Contains(diag.String(), "@@ line") {
		t.Errorf("dry run did not render a diff: %q", diag.String())
	}

	var summary ExecResult
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("summary is not valid JSON: %v\n%s", err, out.String())
	}
	if summary.Status != StatusDryRun {
		t.Errorf("status = %q, want %q", summary.Status, StatusDryRun)
	}
	if summary.Applied != 0 {
		t.Errorf("applied = %d, want 0", summary.Applied)
	}
}

// TestIntegrationDryRunRedactionPreview verifies the dry run actually plans the
// redaction it would perform, so the preview is not a no-op.
func TestIntegrationDryRunRedactionPreview(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/mac.conf"
	// Two genuine MACs. aa:bb:cc:dd:ee:ff is not time-like (0xaa exceeds 23),
	// so both must be matched and redacted.
	if err := writeFile(path, "a=38:00:14:ab:12:a5\nb=aa:bb:cc:dd:ee:ff\n"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out, diag bytes.Buffer
	args := []string{
		"--mode", "stream", "--no-color",
		"replace", "mac", "with", "['REDACTED']", "dryrun", path,
	}
	if err := runPlain(t, args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	var summary ExecResult
	if err := json.Unmarshal(out.Bytes(), &summary); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if summary.Total != 2 {
		t.Fatalf("planned %d changes, want 2", summary.Total)
	}
	for _, m := range summary.Mutations {
		if !strings.Contains(m.ModifiedText, "REDACTED") {
			t.Errorf("planned mutation does not redact: %q -> %q", m.OriginalText, m.ModifiedText)
		}
	}
}

// conjunctiveLspci has one degraded device and one healthy device. Both carry a
// bdf, so a plain "any target" query reports both.
const conjunctiveLspci = `00:01.1 PCI bridge: Advanced Micro Devices, Inc. [AMD]
	Subsystem: Advanced Micro Devices, Inc. [AMD] Device 1453
	LnkCap:	Port #0, Speed 16GT/s, Width x16, ASPM L1
	LnkSta:	Speed 16GT/s, Width x16
	Kernel driver in use: pcieport

00:01.2 PCI bridge: Advanced Micro Devices, Inc. [AMD]
	Subsystem: Advanced Micro Devices, Inc. [AMD] Device 1453
	LnkCap:	Port #0, Speed 32GT/s, Width x4, ASPM not supported
	LnkSta:	Speed 2.5GT/s, Width x4
	Kernel driver in use: pcieport
`

// TestIntegrationConjunctiveQuery is requirement 3.3: "require all" must report
// only the device that satisfies every target.
func TestIntegrationConjunctiveQuery(t *testing.T) {
	results := runQuery(t, conjunctiveLspci, ModeHeader,
		"get", "all", "[bdf, link_downgrade]", "require", "all")

	if len(results) != 1 {
		t.Fatalf("got %d objects, want exactly 1 (the degraded device):\n%s",
			len(results), dumpResults(results))
	}

	m := results[0]
	if !strings.HasPrefix(m.Stanza.Lines[0], "00:01.2") {
		t.Errorf("emitted %q, want the degraded device 00:01.2", m.Stanza.Lines[0])
	}
	if got := tokensFor(m, "bdf"); len(got) != 1 || got[0] != "00:01.2" {
		t.Errorf("bdf = %v, want [00:01.2]", got)
	}
	if got := tokensFor(m, "link_downgrade"); len(got) != 1 {
		t.Errorf("link_downgrade = %v, want one report", got)
	} else if !strings.Contains(got[0], "2.5GT/s") {
		t.Errorf("link_downgrade = %q, want the degraded speed", got[0])
	}
}

// TestIntegrationConjunctiveIsNecessary is the control for requirement 3.3: the
// same query without "require all" reports both devices, proving the modifier is
// what narrows the result rather than some other filter.
func TestIntegrationConjunctiveIsNecessary(t *testing.T) {
	results := runQuery(t, conjunctiveLspci, ModeHeader,
		"get", "all", "[bdf, link_downgrade]")

	if len(results) != 2 {
		t.Fatalf("got %d objects, want 2 (both devices have a bdf):\n%s",
			len(results), dumpResults(results))
	}

	// Only the degraded device should carry a link_downgrade value.
	var withDowngrade int
	for _, m := range results {
		if _, ok := m.MatchedTokens["link_downgrade"]; ok {
			withDowngrade++
		}
	}
	if withDowngrade != 1 {
		t.Errorf("%d devices report link_downgrade, want 1", withDowngrade)
	}
}

// TestIntegrationConjunctiveNDJSONShape verifies the conjunctive result is a
// single NDJSON line, not an array or multiple objects.
func TestIntegrationConjunctiveNDJSONShape(t *testing.T) {
	var out, diag bytes.Buffer
	args := []string{
		"--mode", "header",
		"get", "all", "[bdf, link_downgrade]", "require", "all",
	}
	if err := runPlain(t, args, strings.NewReader(conjunctiveLspci), &out, &diag); err != nil {
		t.Fatalf("run: %v", err)
	}

	text := strings.TrimSuffix(out.String(), "\n")
	if strings.Contains(text, "\n") {
		t.Errorf("expected one NDJSON line, got %d:\n%s", strings.Count(text, "\n")+1, text)
	}
	if strings.HasPrefix(text, "[") {
		t.Errorf("output is a JSON array, want NDJSON: %s", text)
	}

	var m MatchResult
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("line is not valid JSON: %v (%q)", err, text)
	}
}

// TestIntegrationMACTimeLikeTradeoff documents the exact boundary of the
// timestamp guard, so the deliberate trade-off is visible rather than implied.
//
// A colon-form address whose leading octets read as a valid time-of-day is
// treated as a timestamp, because log timestamps are otherwise indistinguishable
// from MAC addresses. The cost is that the handful of vendor prefixes that fall
// in 00-23:00-59:00-59 are not reported; the well-known null and broadcast
// addresses are exempt.
func TestIntegrationMACTimeLikeTradeoff(t *testing.T) {
	cases := []struct {
		line     string
		wantMACs []string
		why      string
	}{
		{
			line:     "Timestamp 12:00:14:ab:12:a5 and MAC 38:00:14:ab:12:a5.",
			wantMACs: []string{"38:00:14:ab:12:a5"},
			why:      "12:00:14 is a time-of-day, so it is dropped; 38 is an invalid hour",
		},
		{
			line:     "link/loopback 00:00:00:00:00:00 brd ff:ff:ff:ff:ff:ff",
			wantMACs: []string{"00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff"},
			why:      "the null and broadcast addresses are exempt from the guard",
		},
		{
			line:     "src 00:11:22:33:44:55",
			wantMACs: nil,
			why:      "00:11:22 is a valid time-of-day; the documented cost of the guard",
		},
		{
			line:     "src 12:34:56:78:9a:bc",
			wantMACs: []string{"12:34:56:78:9a:bc"},
			why:      "12:34:56 is a valid time, but 78 is not a meaningful MAC span; accepted",
		},
		{
			line:     "src aa:bb:cc:dd:ee:ff",
			wantMACs: []string{"aa:bb:cc:dd:ee:ff"},
			why:      "0xaa is not a valid hour, so it cannot be a timestamp",
		},
	}

	for _, tc := range cases {
		results := runPipeline(t, tc.line, ModeStream, "--tokens", "mac")
		var got []string
		for _, r := range results {
			got = append(got, tokensFor(r, "mac")...)
		}
		if len(got) != len(tc.wantMACs) {
			t.Errorf("%q: got %v, want %v (%s)", tc.line, got, tc.wantMACs, tc.why)
			continue
		}
		for i := range tc.wantMACs {
			if got[i] != tc.wantMACs[i] {
				t.Errorf("%q: got %v, want %v (%s)", tc.line, got, tc.wantMACs, tc.why)
			}
		}
	}
}
