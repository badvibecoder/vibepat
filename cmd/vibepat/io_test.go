package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a test helper that writes content to path with owner-only
// permissions.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// TestOpenInputEmptyFile verifies an empty file is a valid, zero-line input
// rather than an error.
func TestOpenInputEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	src, err := OpenInput(path)
	if err != nil {
		t.Fatalf("OpenInput(%q) returned error: %v", path, err)
	}
	defer src.Close()

	var lines int
	sc := src.NewScanner()
	for sc.Scan() {
		lines++
	}

	if err := sc.Err(); err != nil {
		t.Fatalf("scanning empty file: %v", err)
	}
	if lines != 0 {
		t.Fatalf("empty file yielded %d lines, want 0", lines)
	}
}

// TestOpenInputMissingFile verifies a bad path is reported, not silently treated
// as empty input.
func TestOpenInputMissingFile(t *testing.T) {
	_, err := OpenInput(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	if err == nil {
		t.Fatal("OpenInput on a missing file returned nil error")
	}
}

// TestOpenInputDirectory verifies a directory argument is rejected with a clear
// error instead of producing confusing read failures later.
func TestOpenInputDirectory(t *testing.T) {
	_, err := OpenInput(t.TempDir())
	if err == nil {
		t.Fatal("OpenInput on a directory returned nil error")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("directory error = %q, want it to mention %q", err, "directory")
	}
}

// TestOpenInputStdin verifies the stdin path (empty filename) is selected and
// that closing it is a no-op, so `defer src.Close()` is always safe.
func TestOpenInputStdin(t *testing.T) {
	src, err := OpenInput("")
	if err != nil {
		t.Fatalf("OpenInput(\"\") returned error: %v", err)
	}
	if src.Name != "<stdin>" {
		t.Fatalf("Name = %q, want %q", src.Name, "<stdin>")
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close() on stdin returned error: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("second Close() returned error: %v", err)
	}
}

// TestOpenInputStreamsLines verifies the scanner splits lines and strips the
// trailing newline while preserving interior blank lines.
func TestOpenInputStreamsLines(t *testing.T) {
	const content = "alpha\n\nbeta\ngamma"

	path := filepath.Join(t.TempDir(), "lines.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	src, err := OpenInput(path)
	if err != nil {
		t.Fatalf("OpenInput: %v", err)
	}
	defer src.Close()

	var got []string
	sc := src.NewScanner()
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	want := []string{"alpha", "", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d lines %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestScannerHandlesLongLines verifies the raised buffer limit: a line far
// larger than bufio.Scanner's 64 KiB default must not error.
func TestScannerHandlesLongLines(t *testing.T) {
	long := strings.Repeat("x", 512*1024) // 512 KiB, 8x the default limit.
	path := filepath.Join(t.TempDir(), "long.txt")
	if err := os.WriteFile(path, []byte(long+"\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	src, err := OpenInput(path)
	if err != nil {
		t.Fatalf("OpenInput: %v", err)
	}
	defer src.Close()

	sc := src.NewScanner()
	if !sc.Scan() {
		t.Fatalf("Scan() returned false; err = %v", sc.Err())
	}
	if got := len(sc.Text()); got != len(long) {
		t.Fatalf("line length = %d, want %d", got, len(long))
	}
}

// TestScannerFromReader verifies InputSource works over an arbitrary reader, the
// shape the in-process tests rely on for stdin simulation.
func TestScannerFromReader(t *testing.T) {
	src := &InputSource{Name: "<test>", Reader: strings.NewReader("a\nb\n")}
	defer src.Close()

	var got []string
	sc := bufio.NewScanner(src.Reader)
	for sc.Scan() {
		got = append(got, sc.Text())
	}

	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %q, want [a b]", got)
	}
}
