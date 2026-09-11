package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot resolves a path relative to the module root, which is two levels above
// the cmd/vibepat package directory. Tests run from their own package directory,
// so a bare "Makefile" would not resolve.
func repoRoot(t *testing.T, parts ...string) string {
	t.Helper()
	return filepath.Join(append([]string{"..", ".."}, parts...)...)
}

// These tests guard the release build system. A release that silently stops
// cross-compiling is the kind of failure that is only discovered when someone
// tries to run the artifact, so the properties are asserted here instead.

// TestWindowsCrossCompiles is the regression gate for a real break: the terminal
// detection originally called golang.org/x/sys/unix directly, which does not
// exist on Windows, so the windows/amd64 target did not compile at all.
func TestWindowsCrossCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-compile in short mode")
	}

	out := filepath.Join(t.TempDir(), "vibepat.exe")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/vibepat")
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS=windows",
		"GOARCH=amd64",
	)
	cmd.Dir = repoRoot(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("windows/amd64 build failed: %v\n%s", err, output)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("no binary produced: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("windows binary is empty")
	}

	// A PE executable starts with the "MZ" DOS header.
	header := make([]byte, 2)
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := f.Read(header); err != nil {
		t.Fatalf("read header: %v", err)
	}
	if string(header) != "MZ" {
		t.Errorf("output does not start with a PE header: %q", header)
	}
}

// TestLinuxReleaseIsStaticallyLinked verifies the property that makes the Linux
// binary portable across distributions: no dynamic dependencies at all.
func TestLinuxReleaseIsStaticallyLinked(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-compile in short mode")
	}
	if runtime.GOOS != "linux" {
		t.Skip("ELF inspection requires a linux host")
	}

	out := filepath.Join(t.TempDir(), "vibepat-linux-amd64")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./cmd/vibepat")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	cmd.Dir = repoRoot(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("linux/amd64 build failed: %v\n%s", err, output)
	}

	// `file` reports "statically linked" for a pure-Go binary built with
	// CGO_ENABLED=0, and "dynamically linked" as soon as cgo is involved.
	fileOut, err := exec.Command("file", out).CombinedOutput()
	if err != nil {
		t.Skipf("file is unavailable: %v", err)
	}
	if !strings.Contains(string(fileOut), "statically linked") {
		t.Errorf("linux binary is not statically linked: %s", strings.TrimSpace(string(fileOut)))
	}

	// ldd is the authoritative check and fails loudly on a dynamic binary.
	if _, err := exec.LookPath("ldd"); err == nil {
		lddOut, _ := exec.Command("ldd", out).CombinedOutput()
		text := string(lddOut)
		if !strings.Contains(text, "not a dynamic executable") && !strings.Contains(text, "statically linked") {
			t.Errorf("linux binary has dynamic dependencies:\n%s", text)
		}
	}
}

// TestReleaseScriptIsExecutable verifies the script is committed with the right
// mode, since a non-executable script makes `make release` fail confusingly.
func TestReleaseScriptIsExecutable(t *testing.T) {
	script := repoRoot(t, "scripts", "build_release.sh")

	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat %s: %v", script, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("%s is not executable (mode %o)", script, info.Mode().Perm())
	}

	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	body := string(data)

	// The two properties the release must guarantee.
	for _, want := range []string{"CGO_ENABLED=0", "-s -w", "sha256sum", "linux", "windows", "amd64"} {
		if !strings.Contains(body, want) {
			t.Errorf("%s does not mention %q", script, want)
		}
	}

	// It must pin the local caches rather than relying on the global ones.
	for _, want := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		if !strings.Contains(body, want) {
			t.Errorf("%s does not pin %s", script, want)
		}
	}
}

// TestMakefileDefinesReleaseTargets verifies the documented entry points exist.
func TestMakefileDefinesReleaseTargets(t *testing.T) {
	data, err := os.ReadFile(repoRoot(t, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	body := string(data)

	for _, target := range []string{"release:", "release-clean:", "release-verify:", "dist-clean:"} {
		if !strings.Contains(body, target) {
			t.Errorf("Makefile does not define %q", target)
		}
	}
	for _, want := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "CGO_ENABLED"} {
		if !strings.Contains(body, want) {
			t.Errorf("Makefile does not pin %s", want)
		}
	}
}

// TestVersionIsLinkerInjectable verifies the version is a variable rather than a
// constant, because the linker cannot override a constant and the release would
// silently ship an unstamped binary.
func TestVersionIsLinkerInjectable(t *testing.T) {
	data, err := os.ReadFile(repoRoot(t, "cmd", "vibepat", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(data), "const version =") {
		t.Error("version is declared const; -X main.version would have no effect")
	}
	if !strings.Contains(string(data), "var version =") {
		t.Error("version is not declared as an overridable var")
	}
}
