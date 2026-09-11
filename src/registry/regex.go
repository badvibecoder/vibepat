package registry

import (
	"regexp"
)

// RegexMatcher is a token backed by a user-supplied regular expression. It is
// used by the custom token loader and exported so tests and callers can register
// their own patterns without touching package internals.
//
// It is safe for concurrent use and caches nothing beyond the compiled program.
type RegexMatcher struct {
	// Name is the token name this matcher is registered under.
	Name string
	// Description is the human-readable summary shown by TAB completion.
	Description string

	pattern *regexp.Regexp
}

// NewRegexMatcher compiles pattern and returns a matcher for it. An invalid
// pattern is reported rather than silently ignored, so a typo in custom.yaml
// surfaces at startup instead of quietly matching nothing.
func NewRegexMatcher(name, pattern, description string) (*RegexMatcher, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	if description == "" {
		description = name + " - custom regex token"
	}
	return &RegexMatcher{Name: name, Description: description, pattern: re}, nil
}

// Match returns every occurrence of the pattern in text. When the pattern has
// capture groups the first group is returned instead of the whole match, so
// `node(\d+)` yields "0" rather than "node0".
func (m *RegexMatcher) Match(text string) []string {
	if m == nil || m.pattern == nil {
		return nil
	}

	if m.pattern.NumSubexp() == 0 {
		return dedupe(m.pattern.FindAllString(text, -1))
	}

	subs := m.pattern.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		if len(s) < 2 {
			continue
		}
		// Use the first non-empty capture group.
		value := ""
		for _, g := range s[1:] {
			if g != "" {
				value = g
				break
			}
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return dedupe(out)
}

// Describe returns the matcher's human-readable description.
func (m *RegexMatcher) Describe() string {
	if m == nil {
		return ""
	}
	return m.Description
}

// Pattern returns the source text of the compiled expression.
func (m *RegexMatcher) Pattern() string {
	if m == nil || m.pattern == nil {
		return ""
	}
	return m.pattern.String()
}

// dedupe removes repeats while preserving first-seen order, so a token listing
// does not report the same value twice for a repeated occurrence.
func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
