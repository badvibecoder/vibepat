package registry

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Regex matcher candidates.
//
// These patterns exist only to *extract candidate substrings*. The actual
// accept/reject decision for every one of them is made by the validation
// function that follows, so the patterns are deliberately permissive and the
// validators are strict. That split is what keeps 999.888.777.666 out while
// still finding addresses embedded in punctuation.
var (
	// ipv4Candidate matches four dot-separated runs of up to three digits.
	ipv4Candidate = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

	// ipv6Candidate matches a run of hex digits and colons containing at least
	// two colons. Requiring a digit-hex start and two colons is what prevents
	// "12:00:00" timestamps from being treated as addresses; note that the
	// uppercase-only alternative is excluded by [0-9a-fA-F] being case-folded
	// on both ends, so the two-colon rule does the real work.
	ipv6Candidate = regexp.MustCompile(`[0-9a-fA-F][0-9a-fA-F:]*:[0-9a-fA-F:]*:[0-9a-fA-F]+`)

	// macColonCandidate matches aa:bb:cc:dd:ee:ff.
	macColonCandidate = regexp.MustCompile(`\b[0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){5}\b`)

	// macDashCandidate matches aa-bb-cc-dd-ee-ff.
	macDashCandidate = regexp.MustCompile(`\b[0-9a-fA-F]{2}(?:-[0-9a-fA-F]{2}){5}\b`)

	// macCiscoCandidate matches Cisco's dotted form aabb.ccdd.eeff.
	macCiscoCandidate = regexp.MustCompile(`\b[0-9a-fA-F]{4}\.[0-9a-fA-F]{4}\.[0-9a-fA-F]{4}\b`)

	// errorStructuredCandidate matches structured, unambiguous error signal:
	// bracketed log levels and "ERR:"/"ERROR:"-style prefixes. It deliberately
	// does not match a bare "error" word.
	//
	// Every alternative captures the keyword itself, so a match is the bare
	// keyword ("ERR", "failed") rather than the surrounding punctuation. Without
	// the capture group the trailing colon would be swallowed by the match and
	// "ERR:" would be reported as "ERR:", inconsistent with the bracketed form.
	//
	// The bracketed alternative must come first so that "[ERROR]" is matched as a
	// whole; otherwise the bare-keyword alternative would also fire on the "ERROR"
	// inside the brackets and report the same signal twice.
	//
	// Go's regexp is RE2 and has no lookahead, so the colon is consumed by the
	// match rather than asserted; the capture group is what keeps it out of the
	// reported value.
	errorStructuredCandidate = regexp.MustCompile(
		`(?i)\[(error|err|critical|crit|fatal|panic)\]` +
			`|\b(err|error):` +
			`|\b(critical|fatal|panic|failed|failure)\b`)

	// errorLooseCandidate adds a bare case-insensitive "error" word to the
	// structured set. It is the opt-in "catch everything unstructured" mode and
	// deliberately matches negations such as "no errors found"; detecting
	// negation is a natural-language problem this tool does not attempt.
	// The loose pattern admits common inflections ("errors", "errored",
	// "failures") by allowing any word characters after the stem, because a
	// bare-word search that missed the plural would defeat the point of an
	// opt-in catch-everything mode. That breadth is deliberate and it does mean
	// identifiers such as "error_count" match; the strict "error" token is the
	// one to use when that noise matters.
	errorLooseCandidate = regexp.MustCompile(
		`(?i)\[(error|err|critical|crit|fatal|panic)\]` +
			`|\b(error|err)\w*\b` +
			`|\b(critical|fatal|panic)\b` +
			`|\b(fail\w*)\b`)
)

// prefixMatcher extracts candidates with a pattern and keeps only those that
// pass validate.
type prefixMatcher struct {
	name     string
	desc     string
	pattern  *regexp.Regexp
	validate func(string) bool
}

func (m prefixMatcher) Match(text string) []string {
	hits := m.pattern.FindAllString(text, -1)
	if len(hits) == 0 {
		return nil
	}

	out := make([]string, 0, len(hits))
	seen := make(map[string]bool, len(hits))
	for _, h := range hits {
		if !m.validate(h) {
			continue
		}
		// IPv6 extraction can clip a trailing colon off a longer form; trimming
		// here keeps output clean without loosening the pattern.
		h = strings.TrimSuffix(h, ":")
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

func (m prefixMatcher) Describe() string { return m.desc }

// multiPatternMatcher runs several patterns that all feed one token, which the
// MAC matcher needs because the three separator conventions are disjoint.
type multiPatternMatcher struct {
	name     string
	desc     string
	patterns []*regexp.Regexp
	validate func(string) bool
}

func (m multiPatternMatcher) Match(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range m.patterns {
		for _, h := range p.FindAllString(text, -1) {
			if !m.validate(h) || seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func (m multiPatternMatcher) Describe() string { return m.desc }

// funcMatcher adapts a closure, which is the simplest shape for tokens whose
// extraction is not a single regex pass.
type funcMatcher struct {
	name string
	desc string
	fn   func(string) []string
}

func (m funcMatcher) Match(text string) []string { return m.fn(text) }
func (m funcMatcher) Describe() string           { return m.desc }

// ValidIP reports whether s is a syntactically valid IPv4 or IPv6 address.
//
// Validation is delegated to net/netip rather than a hand-written octet count,
// because netip.ParseAddr already enforces the mathematical rules exactly:
// octets must be 0-255, there must be exactly four of them, and leading zeros
// are rejected. Wrap the string in brackets first so a bare IPv6 address is
// parsed as a host rather than as host:port.
func ValidIP(s string) bool {
	if s == "" {
		return false
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if strings.Contains(s, ":") {
		if _, err := netip.ParseAddr("[" + s + "]"); err == nil {
			return true
		}
	}
	return false
}

// ValidMAC reports whether s is a MAC address in colon, dash, or Cisco dotted
// form, and whether it is a real address rather than a mask.
func ValidMAC(s string) bool {
	compact := strings.NewReplacer(":", "", "-", "", ".", "").Replace(s)
	if len(compact) != 12 {
		return false
	}
	for _, r := range compact {
		if !isHexDigit(r) {
			return false
		}
	}
	return true
}

// isTimeLikeMAC reports whether a colon-separated address reads as a
// time-of-day, as in "12:00:14:ab:12:a5" where the leading "12:00:14" is a
// timestamp fragment rather than a vendor prefix.
//
// The first three octets must parse as HH:MM:SS with each field in range. The
// all-zero and all-ff addresses are exempt because they are the well-known null
// and broadcast addresses and appear in real output such as `ip link`:
//
//	link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
//
// The rule is a heuristic, not a proof. An address such as
// "23:59:59:00:00:00" satisfies both readings and is accepted as a MAC, since
// rejecting a valid address is worse than reporting an extra candidate.
func isTimeLikeMAC(s string) bool {
	if strings.Count(s, ":") != 5 {
		return false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return false
	}

	fields := make([]int, 0, 6)
	for _, p := range parts {
		v, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return false
		}
		fields = append(fields, int(v))
	}

	allZero, allFF := true, true
	for _, v := range fields {
		if v != 0 {
			allZero = false
		}
		if v != 0xff {
			allFF = false
		}
	}
	if allZero || allFF {
		return false
	}

	return fields[0] <= 23 && fields[1] <= 59 && fields[2] <= 59
}

// isHexDigit reports whether r is an ASCII hexadecimal digit.
func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// matchIP finds every valid IPv4 and IPv6 address in text.
func matchIP(text string) []string {
	var out []string
	seen := map[string]bool{}

	for _, h := range ipv4Candidate.FindAllString(text, -1) {
		if !ValidIP(h) || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range ipv6Candidate.FindAllString(text, -1) {
		h = strings.TrimSuffix(h, ":")
		if !ValidIP(h) || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// matchMAC finds every MAC address in colon, dash, or Cisco dotted form.
//
// The three separator conventions are disjoint patterns rather than one clever
// expression, because a combined pattern would have to permit mixed separators
// within a single address, which is never valid.
//
// A colon-form candidate whose leading octets read as a time-of-day is dropped,
// because log timestamps are otherwise indistinguishable from MAC addresses.
func matchMAC(text string) []string {
	candidates := multiPatternMatcher{
		patterns: []*regexp.Regexp{macColonCandidate, macDashCandidate, macCiscoCandidate},
		validate: ValidMAC,
	}.Match(text)

	out := candidates[:0:0]
	for _, c := range candidates {
		if isTimeLikeMAC(c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// init registers the built-in networking and log tokens.
func init() {
	Register("ip", funcMatcher{
		name: "ip",
		desc: "ip - IPv4 or IPv6 address, octet-validated",
		fn:   matchIP,
	})
	Register("mac", funcMatcher{
		name: "mac",
		desc: "mac - MAC address (colon, dash, or Cisco dotted)",
		fn:   matchMAC,
	})
	// The log matchers are RegexMatcher values so their capture groups are used:
	// a match is reported as the bare keyword rather than the punctuation around
	// it, and duplicates collapse.
	Register("error", mustRegexMatcher("error",
		"error - structured error signal ([ERROR], ERR:, failed, critical)",
		errorStructuredCandidate))
	Register("error_loose", mustRegexMatcher("error_loose",
		"error_loose - structured errors plus any bare 'error' word",
		errorLooseCandidate))
}

// mustRegexMatcher wraps an already-compiled pattern in a RegexMatcher.
// NewRegexMatcher recompiles from source, which cannot fail here because the
// pattern came from regexp.MustCompile, but returning an error keeps the
// construction honest.
func mustRegexMatcher(name, description string, re *regexp.Regexp) Matcher {
	m, err := NewRegexMatcher(name, re.String(), description)
	if err != nil {
		panic(fmt.Sprintf("registry: built-in pattern for %q failed to compile: %v", name, err))
	}
	return m
}
