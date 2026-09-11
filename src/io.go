package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
)

// DefaultScannerBuffer is the initial read buffer size hint handed to
// bufio.Scanner. We raise Go's 64 KiB default because `lspci -vvv`, `ip -d a`,
// and JVM stack dumps routinely emit single lines far longer than that.
const DefaultScannerBuffer = 1 << 20 // 1 MiB

// maxScannerBuffer caps a single line at 16 MiB so a pathological input cannot
// drive the process out of memory.
const maxScannerBuffer = 16 << 20

// InputSource is an opened input stream plus a NewScanner factory that yields
// lines. It owns the underlying file handle when one was opened for a path
// argument, so callers must Close it.
type InputSource struct {
	// Name is the file path, or "<stdin>" when reading standard input. It is
	// suitable for diagnostics and error messages.
	Name string
	// Reader is the underlying reader. Read it through NewScanner rather than
	// directly, so that all phases agree on line-splitting behavior.
	Reader io.Reader

	closer io.Closer
}

// OpenInput resolves the process's input source.
//
// If path is empty, standard input is used. Otherwise path is opened as a
// regular file; directories, missing files, and permission failures are
// reported as descriptive errors rather than being silently treated as empty
// input.
//
// The returned source must be Closed by the caller. Closing a stdin-backed
// source is a no-op, so it is always safe to defer Close.
func OpenInput(path string) (*InputSource, error) {
	if path == "" {
		return &InputSource{Name: "<stdin>", Reader: os.Stdin}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		// os.Open already reports the path and the reason, so wrap without
		// restating them.
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, fmt.Errorf("open %s: %w", path, errors.New("is a directory, not a file"))
	}

	return &InputSource{Name: path, Reader: f, closer: f}, nil
}

// NewScanner returns a line scanner over the source configured with vibepat's
// buffer sizing. It preserves lines verbatim, including blank ones; blank-line
// handling is a Phase 2 chunking concern, not an input concern.
func (src *InputSource) NewScanner() *bufio.Scanner {
	sc := bufio.NewScanner(src.Reader)
	sc.Buffer(make([]byte, 0, DefaultScannerBuffer), maxScannerBuffer)
	return sc
}

// Close releases the underlying file handle, or does nothing for standard
// input. It is idempotent.
func (src *InputSource) Close() error {
	if src.closer == nil {
		return nil
	}
	err := src.closer.Close()
	src.closer = nil
	return err
}
