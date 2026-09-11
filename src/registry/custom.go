package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CustomConfigDir is the directory under the user's home where custom token
// definitions live.
const CustomConfigDir = ".vibepat"

// CustomConfigName is the custom token definition file.
const CustomConfigName = "custom.yaml"

// maxWorldWritableBits matches the world-writable permission bits. A
// world-writable config can be rewritten by any local user to inject arbitrary
// patterns into a root-run tool, so it is refused rather than trusted.
const maxWorldWritableBits = 0o002

// customFile is the top-level shape of custom.yaml:
//
//	tokens:
//	  sitename:
//	    regex: '\bsite\s+(\S+)'
//	    description: site code     # optional
//	  serial:
//	    regex: 'SN[:=]\s*(\w+)'
//
// Quote regexes with SINGLE quotes. In a YAML double-quoted scalar, "\s" is an
// invalid escape sequence and the file fails to parse, which is a confusing
// failure for a config the user just wrote. Single-quoted YAML takes backslashes
// literally, which is almost always what a regex author wants.
type customFile struct {
	Tokens map[string]customToken `yaml:"tokens"`
}

// customToken is one entry under `tokens:`.
type customToken struct {
	Regex       string `yaml:"regex"`
	Description string `yaml:"description"`
}

// CustomPath returns the path to the custom token file for the current user, or
// an error when the home directory cannot be determined.
func CustomPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, CustomConfigDir, CustomConfigName), nil
}

// LoadCustom reads path and registers every token it defines.
//
// It returns the number of tokens registered. A missing file is not an error
// (most users will not have one); a malformed file, an invalid regex, or an
// attempt to redefine a built-in token is, because silently ignoring a broken
// custom token definition would make the tool quietly stop matching something
// the operator asked for.
//
// Built-in tokens cannot be shadowed. A custom definition whose name collides
// with a built-in is rejected for the same reason.
func LoadCustom(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read custom tokens %s: %w", path, err)
	}

	if err := checkCustomPermissions(path); err != nil {
		return 0, err
	}

	var cfg customFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("parse custom tokens %s: %w", path, err)
	}
	if len(cfg.Tokens) == 0 {
		return 0, nil
	}

	// Register in sorted order so a failure message is deterministic.
	names := make([]string, 0, len(cfg.Tokens))
	for name := range cfg.Tokens {
		names = append(names, name)
	}
	sort.Strings(names)

	registered := 0
	for _, name := range names {
		def := cfg.Tokens[name]

		key := normalizeName(name)
		if key == "" {
			return registered, fmt.Errorf("custom tokens %s: empty token name", path)
		}
		if def.Regex == "" {
			return registered, fmt.Errorf("custom tokens %s: token %q has no regex", path, name)
		}
		if IsRegistered(key) {
			return registered, fmt.Errorf(
				"custom tokens %s: token %q would shadow a built-in token and is not allowed", path, name)
		}

		matcher, err := NewRegexMatcher(key, def.Regex, def.Description)
		if err != nil {
			return registered, fmt.Errorf("custom tokens %s: token %q has an invalid regex: %w", path, name, err)
		}
		Register(key, matcher)
		registered++
	}
	return registered, nil
}

// LoadCustomDefault loads the custom token file from the user's home directory.
// A missing home directory or a missing file both yield zero tokens and no
// error, since custom tokens are strictly optional.
func LoadCustomDefault() (int, error) {
	path, err := CustomPath()
	if err != nil {
		return 0, nil
	}
	return LoadCustom(path)
}

// checkCustomPermissions refuses to trust a world-writable custom token file.
func checkCustomPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat custom tokens %s: %w", path, err)
	}
	if info.Mode().Perm()&maxWorldWritableBits != 0 {
		return fmt.Errorf(
			"custom tokens %s is world-writable (mode %o); refusing to load patterns from a file any user can modify",
			path, info.Mode().Perm())
	}
	return nil
}

// Unregister removes a token. It exists for tests and for callers that load a
// custom file, change it, and reload.
func Unregister(name string) {
	key := normalizeName(name)
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(Registry, key)
	delete(descriptors, key)
}

// SplitTokenList parses a comma-separated token list, trimming blanks.
// It returns nil for an empty string so callers can distinguish "unset" from
// "explicitly empty".
func SplitTokenList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
