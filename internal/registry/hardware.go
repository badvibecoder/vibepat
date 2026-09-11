package registry

import (
	"regexp"
	"strings"
)

// Hardware token patterns: PCI topology and NUMA placement.
var (
	// bdfCandidate matches a PCI Bus:Device.Function identifier with an
	// optional four-hex-digit domain, e.g. "0000:41:00.0", "41:00.0". The
	// function nibble is restricted to 0-7, which is what separates a real BDF
	// from an arbitrary hex pair such as a timestamp fragment.
	bdfCandidate = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{4}:)?[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]\b`)

	// numaCandidate matches "NUMA node: 0" and siblings such as
	// "NUMA node(s): 2" or "NUMA nodes: 2". The label may carry suffixes, but
	// the first integer after the colon is the node or total count.
	numaCandidate = regexp.MustCompile(`(?i)\bNUMA\b[^:\n]*:\s*(\d+)`)
)

// ValidBDF reports whether s is a well-formed PCI Bus:Device.Function
// identifier.
//
// This re-checks the invariants rather than trusting the extraction pattern, so
// it also holds for the CLI and for callers that pass a BDF directly:
//
//   - the function nibble must be 0-7 (a PCI function has no higher values)
//   - the device number must not be 0xff, which lspci never emits and which
//     usually means an unrelated hex run was captured
func ValidBDF(s string) bool {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return false
	}
	rest := s[idx+1:]

	dot := strings.Index(rest, ".")
	if dot < 0 || dot+2 != len(rest) {
		return false
	}
	device, function := rest[:dot], rest[dot+1:]
	if len(device) != 2 || !isHex(device) {
		return false
	}
	if function < "0" || function > "7" {
		return false
	}
	if strings.EqualFold(device, "ff") {
		return false
	}
	return true
}

// isHex reports whether s is entirely ASCII hexadecimal digits.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isHexDigit(r) {
			return false
		}
	}
	return true
}

// matchBDF finds every PCI BDF identifier in text.
func matchBDF(text string) []string {
	return prefixMatcher{pattern: bdfCandidate, validate: ValidBDF}.Match(text)
}

// matchNUMA extracts the node number from every NUMA label in text.
func matchNUMA(text string) []string {
	sub := numaCandidate.FindAllStringSubmatch(text, -1)
	if len(sub) == 0 {
		return nil
	}

	out := make([]string, 0, len(sub))
	seen := map[string]bool{}
	for _, m := range sub {
		if len(m) < 2 || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

// init registers the hardware topology tokens.
func init() {
	Register("bdf", funcMatcher{
		name: "bdf",
		desc: "bdf - PCI Bus:Device.Function ID, e.g. 0000:41:00.0",
		fn:   matchBDF,
	})
	Register("numa", funcMatcher{
		name: "numa",
		desc: "numa - NUMA node number from a 'NUMA node:' label",
		fn:   matchNUMA,
	})
}
