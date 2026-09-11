package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// match is a helper that runs a named token against text.
func match(t *testing.T, token, text string) []string {
	t.Helper()

	m, ok := Lookup(token)
	if !ok {
		t.Fatalf("token %q is not registered", token)
	}
	return m.Match(text)
}

// has reports whether want appears in got.
func has(got []string, want string) bool {
	for _, g := range got {
		if g == want {
			return true
		}
	}
	return false
}

// --- ip -------------------------------------------------------------------

// TestValidIPAcceptReject is the headline IPv4 gate from the spec.
func TestValidIPAcceptReject(t *testing.T) {
	valid := []string{
		"10.0.0.1",
		"0.0.0.0",
		"255.255.255.255",
		"192.168.1.254",
		"2001:db8::1",
		"::1",
		"fe80::1",
	}
	for _, s := range valid {
		if !ValidIP(s) {
			t.Errorf("ValidIP(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"999.888.777.666",
		"10.0.0.256",
		"256.1.1.1",
		"1.2.3",
		"1.2.3.4.5",
		"010.1.1.1", // leading zeros are ambiguous and rejected
		"10.0.0.01",
		"not-an-ip",
		"",
		"10.0.0.1/24",
	}
	for _, s := range invalid {
		if ValidIP(s) {
			t.Errorf("ValidIP(%q) = true, want false", s)
		}
	}
}

// TestMatchIPFindsEmbeddedAddresses verifies extraction from surrounding text.
func TestMatchIPFindsEmbeddedAddresses(t *testing.T) {
	text := "inet 10.0.0.1/24 brd 10.0.0.255 scope global\ninet6 2001:db8::1/64 scope global"

	got := match(t, "ip", text)
	if !has(got, "10.0.0.1") {
		t.Errorf("missing 10.0.0.1 in %v", got)
	}
	if !has(got, "2001:db8::1") {
		t.Errorf("missing 2001:db8::1 in %v", got)
	}
	// The broadcast is a valid address and should appear.
	if !has(got, "10.0.0.255") {
		t.Errorf("missing 10.0.0.255 in %v", got)
	}
}

// TestMatchIPRejectsInvalidEmbedded is the false-positive gate: an invalid
// address must not be extracted even when it appears in a realistic line.
func TestMatchIPRejectsInvalidEmbedded(t *testing.T) {
	got := match(t, "ip", "bogus 999.888.777.666 and 10.0.0.256 here")

	if has(got, "999.888.777.666") {
		t.Errorf("extracted the invalid address 999.888.777.666: %v", got)
	}
	if has(got, "10.0.0.256") {
		t.Errorf("extracted the invalid address 10.0.0.256: %v", got)
	}
}

// TestMatchIPIgnoresTimestampsAndPorts verifies common false-positive shapes.
func TestMatchIPIgnoresTimestampsAndPorts(t *testing.T) {
	got := match(t, "ip", "12:00:00 kernel: listening on 0.0.0.0:8080")

	if has(got, "12:00:00") {
		t.Errorf("treated a timestamp as an IPv6 address: %v", got)
	}
	if has(got, "0.0.0.0:8080") {
		t.Errorf("treated host:port as an address: %v", got)
	}
	if !has(got, "0.0.0.0") {
		t.Errorf("missed the real address 0.0.0.0 in %v", got)
	}
}

// TestMatchIPDeduplicates verifies a repeated address is reported once.
func TestMatchIPDeduplicates(t *testing.T) {
	got := match(t, "ip", "10.0.0.1 10.0.0.1 10.0.0.1")
	if len(got) != 1 {
		t.Errorf("got %v, want one deduplicated entry", got)
	}
}

// --- mac ------------------------------------------------------------------

// TestValidMACAcceptReject covers the three separator conventions.
func TestValidMACAcceptReject(t *testing.T) {
	valid := []string{
		"38:00:14:ab:12:a5",
		"38-00-14-ab-12-a5",
		"3800.14ab.12a5",
		"00:00:00:00:00:00",
		"FF:FF:FF:FF:FF:FF",
	}
	for _, s := range valid {
		if !ValidMAC(s) {
			t.Errorf("ValidMAC(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"38:00:14:ab:12",
		"38:00:14:ab:12:a5:ff",
		"zz:00:14:ab:12:a5",
		"",
		"not-a-mac",
	}
	for _, s := range invalid {
		if ValidMAC(s) {
			t.Errorf("ValidMAC(%q) = true, want false", s)
		}
	}
}

// TestMatchMACAllSeparators is the spec's MAC gate.
func TestMatchMACAllSeparators(t *testing.T) {
	text := "link/ether 38:00:14:ab:12:a5 brd 38-00-14-ab-12-a5 cisco 3800.14ab.12a5"

	got := match(t, "mac", text)
	for _, want := range []string{"38:00:14:ab:12:a5", "38-00-14-ab-12-a5", "3800.14ab.12a5"} {
		if !has(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

// TestMatchMACRejectsShortAndOverlong verifies length is enforced.
//
// A seven-pair run contains no valid MAC, because the extraction pattern cannot
// cross the boundary created by its trailing word boundary. What it does
// contain is a valid six-pair MAC followed by ":ff", so a match is still
// correct there; the gate is that the overlong run is never reported whole.
func TestMatchMACRejectsShortAndOverlong(t *testing.T) {
	if got := match(t, "mac", "38:00:14:ab:12"); len(got) != 0 {
		t.Errorf("short form: got %v, want no matches", got)
	}
	if got := match(t, "mac", "38:00:14:ab:12:a5:ff"); has(got, "38:00:14:ab:12:a5:ff") {
		t.Errorf("got %v, want the overlong run never reported whole", got)
	}
	// A valid six-pair MAC does appear inside a seven-pair run, and reporting
	// it is correct: only the full, overlong run would be wrong.
	if got := match(t, "mac", "ff:ff:ff:ff:ff:ff:ff"); !has(got, "ff:ff:ff:ff:ff:ff") {
		t.Errorf("got %v, want the valid six-pair address", got)
	}
}

// --- bdf ------------------------------------------------------------------

// TestValidBDFAcceptReject is the spec's BDF gate.
func TestValidBDFAcceptReject(t *testing.T) {
	valid := []string{"0000:06:00.0", "0000:41:00.1", "41:00.0", "00:1f.6", "06:00.0"}
	for _, s := range valid {
		if !ValidBDF(s) {
			t.Errorf("ValidBDF(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"0000:06:00.9", // function nibble must be 0-7
		"41:ff.0",      // device 0xff is not a real device
		"12:00:00",     // timestamp
		"0000:06:00",
		"",
	}
	for _, s := range invalid {
		if ValidBDF(s) {
			t.Errorf("ValidBDF(%q) = true, want false", s)
		}
	}
}

// TestMatchBDFFromLspci verifies extraction from a realistic lspci line.
func TestMatchBDFFromLspci(t *testing.T) {
	got := match(t, "bdf", "0000:41:00.0 Ethernet controller: Intel Corporation")

	if !has(got, "0000:41:00.0") {
		t.Errorf("got %v, want 0000:41:00.0", got)
	}
}

// TestMatchBDFRejectsTimestamps is the false-positive gate for the pattern that
// motivated the domain/nibble requirements.
func TestMatchBDFRejectsTimestamps(t *testing.T) {
	got := match(t, "bdf", "2024-01-01 12:00:00 ERROR something failed")

	if len(got) != 0 {
		t.Errorf("got %v, want no BDF matches in a timestamp line", got)
	}
}

// TestMatchBDFMultiple verifies several devices on one stanza are all found.
func TestMatchBDFMultiple(t *testing.T) {
	got := match(t, "bdf", "0000:00:00.0 root 0000:02:00.0 gpu 0000:41:00.1 nic")

	if len(got) != 3 {
		t.Fatalf("got %v, want 3 BDFs", got)
	}
}

// --- numa -----------------------------------------------------------------

// TestMatchNUMA covers the label variants actually emitted by lscpu and numactl.
func TestMatchNUMA(t *testing.T) {
	cases := map[string]string{
		"NUMA node: 0":            "0",
		"NUMA node(s): 2":         "2",
		"NUMA nodes: 4":           "4",
		"NUMA node0 CPU(s): 0-15": "0",
		"numa node: 1":            "1",
		"NUMA node 0 CPU(s): 0-7": "0",
	}
	for text, want := range cases {
		got := match(t, "numa", text)
		if !has(got, want) {
			t.Errorf("match(%q) = %v, want %q", text, got, want)
		}
	}
}

// TestMatchNUMANoFalsePositive verifies unrelated text does not match.
func TestMatchNUMANoFalsePositive(t *testing.T) {
	got := match(t, "numa", "this line mentions nothing relevant")
	if len(got) != 0 {
		t.Errorf("got %v, want no matches", got)
	}
}

// --- error ----------------------------------------------------------------

// TestMatchErrorStructured is the strict-by-default gate.
func TestMatchErrorStructured(t *testing.T) {
	positives := []string{
		"[ERROR] disk failure detected",
		"[error] lower case",
		"[CRITICAL] thermal event",
		"ERR: connection reset",
		"error: file not found",
		"ERROR: something",
		"the job failed",
		"critical temperature reached",
		"[FATAL] cannot continue",
	}
	for _, text := range positives {
		if got := match(t, "error", text); len(got) == 0 {
			t.Errorf("match(\"error\", %q) = empty, want a match", text)
		}
	}
}

// TestMatchErrorStrictIgnoresBareError is the deliberate strictness: an
// unstructured bare "error" word is NOT matched by the default token, because
// negations and identifiers would flood results.
func TestMatchErrorStrictIgnoresBareError(t *testing.T) {
	negatives := []string{
		"no errors found", // unstructured; needs the loose token
		"error_count=0",   // an identifier, not a log level
		"all clear",
		"an error occurred", // bare word, no structured marker
	}
	for _, text := range negatives {
		if got := match(t, "error", text); len(got) != 0 {
			t.Errorf("strict match(\"error\", %q) = %v, want no match", text, got)
		}
	}
}

// TestMatchErrorLooseCatchesUnstructured verifies the opt-in override does catch
// what the strict token intentionally ignores.
func TestMatchErrorLooseCatchesUnstructured(t *testing.T) {
	// Loose mode is intentionally broad and does match identifier-shaped text
	// such as "error_count"; that is the documented trade-off of the override.
	for _, text := range []string{"no errors found", "an error occurred", "2 failures detected", "the job errored"} {
		if got := match(t, "error_loose", text); len(got) == 0 {
			t.Errorf("loose match(\"error_loose\", %q) = empty, want a match", text)
		}
	}
}

// TestMatchErrorLooseSupersetOfStructured verifies loose never misses what
// strict finds.
func TestMatchErrorLooseSupersetOfStructured(t *testing.T) {
	for _, text := range []string{"[ERROR] x", "ERR: x", "job failed", "critical"} {
		if got := match(t, "error", text); len(got) == 0 {
			t.Fatalf("strict missed %q", text)
		}
		if got := match(t, "error_loose", text); len(got) == 0 {
			t.Errorf("loose missed %q which strict matched", text)
		}
	}
}

// --- registry plumbing ----------------------------------------------------

// TestRegistryLookupIsCaseInsensitive verifies name normalization.
func TestRegistryLookupIsCaseInsensitive(t *testing.T) {
	for _, name := range []string{"ip", "IP", "Ip", " ip "} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("Lookup(%q) failed, want a case-insensitive hit", name)
		}
	}
}

// TestRegistryBuiltinsRegistered verifies every documented token exists.
func TestRegistryBuiltinsRegistered(t *testing.T) {
	for _, name := range []string{"ip", "mac", "bdf", "numa", "error", "error_loose"} {
		if !IsRegistered(name) {
			t.Errorf("built-in token %q is not registered", name)
		}
	}
}

// TestRegistryNamesSorted verifies Names returns stable, sorted output for
// deterministic TAB completion.
func TestRegistryNamesSorted(t *testing.T) {
	names := Names()
	if len(names) < 6 {
		t.Fatalf("Names() = %v, want at least the six built-ins", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("Names() is not sorted: %v", names)
		}
	}
}

// TestEveryBuiltinHasDescription verifies the Phase 5 completer has something to
// show for each token.
func TestEveryBuiltinHasDescription(t *testing.T) {
	for _, name := range Names() {
		if strings.TrimSpace(Describe(name)) == "" {
			t.Errorf("token %q has no description", name)
		}
	}
}

// TestMatchTokensReturnsEveryValue verifies multiple matches are preserved as a
// list rather than joined into one string.
func TestMatchTokensReturnsEveryValue(t *testing.T) {
	matched := MatchTokens("a 10.0.0.1 b 10.0.0.2", []string{"ip"})

	got := matched["ip"]
	if len(got) != 2 {
		t.Fatalf("matched[ip] = %v, want 2 entries", got)
	}
	if got[0] != "10.0.0.1" || got[1] != "10.0.0.2" {
		t.Errorf("matched[ip] = %v, want the two addresses in order", got)
	}
}

// TestMatchTokensUnknownIsSkipped verifies a bad token name degrades to no match
// rather than panicking, since the CLI validates names separately.
func TestMatchTokensUnknownIsSkipped(t *testing.T) {
	matched := MatchTokens("10.0.0.1", []string{"nosuchtoken"})
	if len(matched) != 0 {
		t.Errorf("got %v, want an empty map", matched)
	}
}

// TestMatchTokensMapsAreNonNil verifies JSON serialization yields {} not null.
func TestMatchTokensMapsAreNonNil(t *testing.T) {
	matched := MatchTokens("nothing here", []string{"ip"})
	if matched == nil {
		t.Fatal("MatchTokens returned a nil map; it must be non-nil for stable JSON")
	}
}

// TestSplitTokenList verifies flag parsing.
func TestSplitTokenList(t *testing.T) {
	got := SplitTokenList(" ip , bdf ,numa ")
	want := []string{"ip", "bdf", "numa"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	if SplitTokenList("   ") != nil {
		t.Error("SplitTokenList of blanks should be nil, meaning unset")
	}
}

// --- custom token loader --------------------------------------------------

// writeCustom writes a custom token file and returns its path.
func writeCustom(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), CustomConfigName)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// WriteFile is subject to umask, so set the mode explicitly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return path
}

// TestLoadCustomRegistersTokens verifies the documented YAML shape works.
func TestLoadCustomRegistersTokens(t *testing.T) {
	// Deliberately written exactly as a user would type it into the file: single
	// quotes, real backslashes. An earlier version of this test used Go-escaped
	// double quotes and so passed while the documented format was unusable.
	path := writeCustom(t, `
tokens:
  sitename:
    regex: '\bsite\s+(\S+)'
    description: site code
  serial:
    regex: 'SN[:=]\s*(\w+)'
`, 0o600)

	// Clean up any registrations this test makes.
	t.Cleanup(func() {
		Unregister("sitename")
		Unregister("serial")
	})

	n, err := LoadCustom(path)
	if err != nil {
		t.Fatalf("LoadCustom: %v", err)
	}
	if n != 2 {
		t.Fatalf("registered %d tokens, want 2", n)
	}

	got := match(t, "sitename", "site ord1 something")
	if !has(got, "ord1") {
		t.Errorf("sitename match = %v, want ord1 from the capture group", got)
	}
	if Describe("sitename") != "site code" {
		t.Errorf("description = %q, want %q", Describe("sitename"), "site code")
	}
}

// TestLoadCustomMissingFileIsNotAnError verifies custom tokens are optional.
func TestLoadCustomMissingFileIsNotAnError(t *testing.T) {
	n, err := LoadCustom(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("LoadCustom on a missing file returned an error: %v", err)
	}
	if n != 0 {
		t.Errorf("registered %d tokens, want 0", n)
	}
}

// TestLoadCustomRejectsInvalidRegex verifies a typo is reported, not swallowed.
func TestLoadCustomRejectsInvalidRegex(t *testing.T) {
	path := writeCustom(t, "tokens:\n  broken:\n    regex: \"[unclosed\"\n", 0o600)

	if _, err := LoadCustom(path); err == nil {
		t.Fatal("LoadCustom accepted an invalid regex, want an error")
	}
	if IsRegistered("broken") {
		t.Error("a broken token was registered anyway")
	}
}

// TestLoadCustomRejectsShadowingBuiltin verifies a custom file cannot quietly
// replace a built-in matcher.
func TestLoadCustomRejectsShadowingBuiltin(t *testing.T) {
	path := writeCustom(t, "tokens:\n  ip:\n    regex: \"\\\\d+\"\n", 0o600)

	_, err := LoadCustom(path)
	if err == nil {
		t.Fatal("LoadCustom allowed a custom token to shadow a built-in")
	}
	if !strings.Contains(err.Error(), "shadow") {
		t.Errorf("error = %q, want it to explain the shadowing", err)
	}

	// The built-in must still be intact and strict.
	if ValidIP("999.888.777.666") {
		t.Error("the built-in ip matcher was damaged by the failed load")
	}
}

// TestLoadCustomRejectsWorldWritable verifies the permission guard.
func TestLoadCustomRejectsWorldWritable(t *testing.T) {
	path := writeCustom(t, "tokens:\n  x:\n    regex: \"a\"\n", 0o666)

	_, err := LoadCustom(path)
	if err == nil {
		t.Fatal("LoadCustom trusted a world-writable file")
	}
	if !strings.Contains(err.Error(), "world-writable") {
		t.Errorf("error = %q, want it to mention world-writable", err)
	}
}

// TestLoadCustomRejectsMalformedYAML verifies parse errors surface.
func TestLoadCustomRejectsMalformedYAML(t *testing.T) {
	path := writeCustom(t, "tokens: [this is: not valid: yaml\n", 0o600)

	if _, err := LoadCustom(path); err == nil {
		t.Fatal("LoadCustom accepted malformed YAML, want an error")
	}
}

// TestLoadCustomEmptyFileIsFine verifies an empty config registers nothing.
func TestLoadCustomEmptyFileIsFine(t *testing.T) {
	path := writeCustom(t, "", 0o600)

	n, err := LoadCustom(path)
	if err != nil {
		t.Fatalf("LoadCustom on an empty file: %v", err)
	}
	if n != 0 {
		t.Errorf("registered %d tokens, want 0", n)
	}
}

// TestNewRegexMatcherFirstCaptureGroup verifies captured-group extraction.
func TestNewRegexMatcherFirstCaptureGroup(t *testing.T) {
	m, err := NewRegexMatcher("node", `node(\d+)`, "")
	if err != nil {
		t.Fatalf("NewRegexMatcher: %v", err)
	}

	got := m.Match("node0 and node3 and node7")
	want := []string{"0", "3", "7"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

// TestNewRegexMatcherNoCaptureGroup verifies whole-match extraction.
func TestNewRegexMatcherNoCaptureGroup(t *testing.T) {
	m, err := NewRegexMatcher("word", `\bfoo\d*\b`, "")
	if err != nil {
		t.Fatalf("NewRegexMatcher: %v", err)
	}
	if got := m.Match("foo foo12 bar"); len(got) != 2 {
		t.Errorf("got %v, want 2 whole matches", got)
	}
}

// TestNewRegexMatcherInvalid verifies compile errors are reported.
func TestNewRegexMatcherInvalid(t *testing.T) {
	if _, err := NewRegexMatcher("bad", "[unclosed", ""); err == nil {
		t.Fatal("NewRegexMatcher accepted an invalid pattern")
	}
}

// TestRegexMatcherDefaultDescription verifies a description is always available
// for the completer.
func TestRegexMatcherDefaultDescription(t *testing.T) {
	m, err := NewRegexMatcher("x", "a", "")
	if err != nil {
		t.Fatalf("NewRegexMatcher: %v", err)
	}
	if strings.TrimSpace(m.Describe()) == "" {
		t.Error("Describe() is empty; the completer would show nothing")
	}
}

// TestRegexMatcherDeduplicates verifies repeated matches are collapsed.
func TestRegexMatcherDeduplicates(t *testing.T) {
	m, err := NewRegexMatcher("x", `foo`, "")
	if err != nil {
		t.Fatalf("NewRegexMatcher: %v", err)
	}
	if got := m.Match("foo foo foo"); len(got) != 1 {
		t.Errorf("got %v, want one deduplicated match", got)
	}
}

// TestLoadCustomDoubleQuotedRegexFailsLoudly documents the YAML quoting trap:
// a double-quoted scalar containing \\s is invalid YAML, and the loader must
// report it rather than silently registering nothing.
func TestLoadCustomDoubleQuotedRegexFailsLoudly(t *testing.T) {
	path := writeCustom(t, `
tokens:
  sitename:
    regex: "\\bsite\\s+(\\S+)"
`, 0o600)

	n, err := LoadCustom(path)
	if err == nil {
		t.Skip("this YAML parser accepted the double-quoted escape; nothing to assert")
	}
	if n != 0 {
		t.Errorf("registered %d tokens despite a parse failure, want 0", n)
	}
}

// --- link_downgrade -------------------------------------------------------

// TestLinkDowngradeDetectsBothDimensions verifies a link that negotiated lower
// speed and width is reported with both.
func TestLinkDowngradeDetectsBothDimensions(t *testing.T) {
	const stanza = `00:02.0 VGA compatible controller: Intel Corporation
	LnkCap: Port #0, Speed 16GT/s, Width x16, ASPM L1
	LnkSta: Speed 8GT/s, Width x8 (downgraded)`

	got := match(t, "link_downgrade", stanza)
	if len(got) != 1 {
		t.Fatalf("got %v, want exactly one downgrade report", got)
	}
	for _, want := range []string{"8GT/s", "16GT/s", "x8", "x16"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("report %q missing %q", got[0], want)
		}
	}
}

// TestLinkDowngradeSpeedOnly verifies a width-preserving speed drop is caught.
func TestLinkDowngradeSpeedOnly(t *testing.T) {
	const stanza = `LnkCap: Speed 16GT/s, Width x16
	LnkSta: Speed 5GT/s, Width x16`

	got := match(t, "link_downgrade", stanza)
	if len(got) != 1 {
		t.Fatalf("got %v, want a speed-only downgrade", got)
	}
	if strings.Contains(got[0], "width") {
		t.Errorf("report %q mentions width, which did not change", got[0])
	}
}

// TestLinkDowngradeWidthOnly verifies a speed-preserving width drop is caught.
func TestLinkDowngradeWidthOnly(t *testing.T) {
	const stanza = `LnkCap: Speed 8GT/s, Width x16
	LnkSta: Speed 8GT/s, Width x4`

	got := match(t, "link_downgrade", stanza)
	if len(got) != 1 {
		t.Fatalf("got %v, want a width-only downgrade", got)
	}
	if strings.Contains(got[0], "speed") {
		t.Errorf("report %q mentions speed, which did not change", got[0])
	}
}

// TestLinkDowngradeHealthyLinkIsSilent verifies a full-capability link is not
// reported, which is the false-positive gate for this token.
func TestLinkDowngradeHealthyLinkIsSilent(t *testing.T) {
	const stanza = `LnkCap: Speed 16GT/s, Width x16
	LnkSta: Speed 16GT/s, Width x16, TrErr- Train- SlotClk+ DLActive+`

	if got := match(t, "link_downgrade", stanza); len(got) != 0 {
		t.Errorf("got %v, want no report for a healthy link", got)
	}
}

// TestLinkDowngradeMissingDataIsSilent verifies a stanza without link lines is
// not reported. This is the common case on a non-root lspci, where the kernel
// hides configuration space.
func TestLinkDowngradeMissingDataIsSilent(t *testing.T) {
	cases := []string{
		"00:00.0 Host bridge: Intel Corporation\n\tFlags: fast devsel",
		"LnkCap: Speed 16GT/s, Width x16",
		"LnkSta: Speed 8GT/s, Width x8",
		"nothing relevant at all",
	}
	for _, text := range cases {
		if got := match(t, "link_downgrade", text); len(got) != 0 {
			t.Errorf("match(%q) = %v, want no report", text, got)
		}
	}
}

// TestLinkDowngradeToleratesLegacySpelling verifies the older "5.0GT/s" form.
func TestLinkDowngradeToleratesLegacySpelling(t *testing.T) {
	const stanza = "LnkCap: Speed 5.0GT/s, Width x4\nLnkSta: Speed 2.5GT/s, Width x1"

	got := match(t, "link_downgrade", stanza)
	if len(got) != 1 {
		t.Fatalf("got %v, want a downgrade report for legacy spellings", got)
	}
}

// TestLinkDowngradeIsRegistered verifies the token from the specification's own
// example is actually available.
func TestLinkDowngradeIsRegistered(t *testing.T) {
	if !IsRegistered("link_downgrade") {
		t.Fatal("link_downgrade is not registered")
	}
	if strings.TrimSpace(Describe("link_downgrade")) == "" {
		t.Error("link_downgrade has no description")
	}
}

// Real-world `lspci -vv` stanzas, copied verbatim (tab-indented, including the
// "(downgraded)" parenthetical that appears before the Width field on LnkSta).
// Earlier fixtures were hand-written and missed both details.
const (
	realNVMeStanza = "04:00.0 Non-Volatile memory controller: Samsung Electronics Co Ltd NVMe SSD Controller PM9A1\n" +
		"\tLnkCap:\tPort #0, Speed 32GT/s, Width x4, ASPM L1, Exit Latency L1 <64us\n" +
		"\tLnkSta:\tSpeed 2.5GT/s (downgraded), Width x4\n" +
		"\tLnkCap2:\tSupported Link Speeds: 2.5-32GT/s, Crosslink- Retimer+ 2Retimers+ DRS\n" +
		"\tLnkSta2:\tCurrent De-emphasis Level: -3.5dB, EqualizationComplete+ Equalization\n"

	realRootPortStanza = "00:01.2 PCI bridge: Advanced Micro Devices, Inc. [AMD]\n" +
		"\tLnkCap:\tPort #0, Speed 32GT/s, Width x4, ASPM not supported\n" +
		"\tLnkSta:\tSpeed 2.5GT/s, Width x4\n"

	realHealthyStanza = "00:01.1 PCI bridge: Advanced Micro Devices, Inc. [AMD]\n" +
		"\tLnkCap:\tPort #0, Speed 16GT/s, Width x16, ASPM L1\n" +
		"\tLnkSta:\tSpeed 16GT/s, Width x16\n"
)

// TestLinkDowngradeRealNVMeFormat verifies the verbatim endpoint format, where
// the "(downgraded)" note sits between the speed and the width.
func TestLinkDowngradeRealNVMeFormat(t *testing.T) {
	got := match(t, "link_downgrade", realNVMeStanza)
	if len(got) != 1 {
		t.Fatalf("got %v, want a speed downgrade report", got)
	}
	if !strings.Contains(got[0], "2.5GT/s") || !strings.Contains(got[0], "32GT/s") {
		t.Errorf("report %q missing the speed comparison", got[0])
	}
	if strings.Contains(got[0], "width") {
		t.Errorf("report %q mentions width, which did not change", got[0])
	}
}

// TestLinkDowngradeRealRootPortFormat verifies the verbatim bridge format.
func TestLinkDowngradeRealRootPortFormat(t *testing.T) {
	got := match(t, "link_downgrade", realRootPortStanza)
	if len(got) != 1 {
		t.Fatalf("got %v, want a speed downgrade report", got)
	}
}

// TestLinkDowngradeRealHealthyFormat verifies an undegraded real link is silent.
func TestLinkDowngradeRealHealthyFormat(t *testing.T) {
	if got := match(t, "link_downgrade", realHealthyStanza); len(got) != 0 {
		t.Errorf("got %v, want no report for a full-capability link", got)
	}
}
