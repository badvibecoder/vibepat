// Package registry holds vibepat's semantic token matchers: the pluggable
// dictionary of datacenter networking, hardware, and log patterns that turn a
// blob of text into named, structured facts.
//
// Matchers are registered by name and looked up case-insensitively. The built-in
// set lives in network.go and hardware.go; user-supplied tokens loaded from
// ~/.vibepat/custom.yaml are added by custom.go and may not shadow a built-in.
package registry

import (
	"sort"
	"strings"
	"sync"
)

// Matcher extracts every occurrence of one semantic token from a chunk of text.
//
// Match receives a whole stanza's text (all lines joined by "\n") and returns
// the matched substrings in the order they appear. An empty or nil result means
// no match. Implementations must be safe for concurrent use and must not retain
// the input string.
type Matcher interface {
	// Match returns every occurrence of the token in text.
	Match(text string) []string
	// Describe returns a one-line human-readable description, used by the
	// Phase 5 interactive completer.
	Describe() string
}

// Canonical built-in token names. The main package refers to these rather than
// repeating string literals, so a rename cannot drift between the two.
const (
	// TokenIP is the IPv4/IPv6 address token.
	TokenIP = "ip"
	// TokenMAC is the MAC address token.
	TokenMAC = "mac"
	// TokenBDF is the PCI Bus:Device.Function token.
	TokenBDF = "bdf"
	// TokenNUMA is the NUMA node token.
	TokenNUMA = "numa"
	// TokenError is the structured error token.
	TokenError = "error"
	// TokenErrorLoose is the opt-in broad error token.
	TokenErrorLoose = "error_loose"
	// TokenLinkDowngrade is the PCIe link degradation token.
	TokenLinkDowngrade = "link_downgrade"
)

// Registry is the process-wide token table. It is safe for concurrent use.
//
// It is intentionally a package-level variable because the built-in matchers
// register themselves in init and Phase 5's completer enumerates it directly.
var Registry = map[string]Matcher{}

var (
	registryMu sync.RWMutex
	// descriptors caches Describe() output so lookups do not need the lock held
	// while calling into a Matcher.
	descriptors = map[string]string{}
)

// Register adds a matcher under name. Names are case-insensitive and normalized
// to lower case. Registering a name that already exists replaces it, which is
// what lets tests swap in fixtures; use IsRegistered first when a collision
// should be an error.
func Register(name string, m Matcher) {
	if m == nil {
		return
	}
	key := normalizeName(name)
	if key == "" {
		return
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	Registry[key] = m
	descriptors[key] = m.Describe()
}

// IsRegistered reports whether name already has a matcher.
func IsRegistered(name string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := Registry[normalizeName(name)]
	return ok
}

// Lookup returns the matcher registered under name.
func Lookup(name string) (Matcher, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	m, ok := Registry[normalizeName(name)]
	return m, ok
}

// Names returns every registered token name, sorted, for stable output and
// deterministic TAB completion.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(Registry))
	for name := range Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Describe returns the description for a token, or "" if it is not registered.
func Describe(name string) string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return descriptors[normalizeName(name)]
}

// MatchTokens runs the named tokens against text and returns every value found
// for each token that matched.
//
// Unknown token names are skipped rather than treated as errors, so a typo in a
// user-supplied token list degrades to "no match" instead of aborting a run.
// The returned map is always non-nil so it serializes as {} rather than null,
// and a token with no matches is omitted entirely rather than present-and-empty.
func MatchTokens(text string, tokens []string) map[string][]string {
	matched := make(map[string][]string, len(tokens))

	for _, name := range tokens {
		key := normalizeName(name)
		m, ok := Lookup(key)
		if !ok {
			continue
		}
		matches := m.Match(text)
		if len(matches) == 0 {
			continue
		}
		matched[key] = matches
	}
	return matched
}

// normalizeName lower-cases and trims a token name.
func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
