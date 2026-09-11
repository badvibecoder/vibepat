package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BackupSuffix is appended to a source path to form its backup path.
const BackupSuffix = ".bak"

// BackupPath returns the backup path for a source file.
func BackupPath(path string) string { return path + BackupSuffix }

// CreateBackup copies path to path+".bak" and returns the backup path.
//
// The backup is written only if it does not already exist, so the original
// contents survive repeated runs. A second `vibepat replace` over the same file
// therefore still leaves the pristine pre-first-run copy on disk. Callers that
// want a fresh backup each time should create a timestamped path themselves.
//
// The copy is durable before it is reported: the backup file is fsynced and its
// directory entry flushed, so a crash immediately after CreateBackup returns
// cannot leave a zero-length or partial backup.
func CreateBackup(path string) (string, error) {
	if path == "" {
		return "", errNotAFile
	}

	backup := BackupPath(path)
	if _, err := os.Stat(backup); err == nil {
		// Already protected by an earlier run; leave it alone.
		return backup, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("check backup %s: %w", backup, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}

	src, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer src.Close()

	// Create with the source's own mode so the backup is not more permissive
	// than the original (config files are often 0600).
	dst, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return "", fmt.Errorf("create backup %s: %w", backup, err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(backup)
		return "", fmt.Errorf("copy %s to %s: %w", path, backup, err)
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		os.Remove(backup)
		return "", fmt.Errorf("sync backup %s: %w", backup, err)
	}
	if err := dst.Close(); err != nil {
		os.Remove(backup)
		return "", fmt.Errorf("close backup %s: %w", backup, err)
	}

	if err := syncDir(filepath.Dir(backup)); err != nil {
		return "", err
	}
	return backup, nil
}

// WriteAtomic replaces path with the given lines.
//
// The new content is written to a temporary file in the same directory, synced,
// and then renamed over the target. Rename is atomic within a filesystem, so a
// crash or a full disk can never leave a half-written config behind: the
// original either remains intact or is replaced wholesale. This matters more
// than speed for files like sshd_config.
//
// The original file's permission bits are preserved.
func WriteAtomic(path string, lines []string) error {
	if path == "" {
		return errNotAFile
	}

	dir := filepath.Dir(path)

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".vibepat-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Best-effort cleanup: if anything below fails, do not leave a stray temp.
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		cleanup()
		return fmt.Errorf("set mode on %s: %w", tmpName, err)
	}

	for i, line := range lines {
		if _, err := tmp.WriteString(line); err != nil {
			cleanup()
			return fmt.Errorf("write %s: %w", tmpName, err)
		}
		// Preserve the input's trailing-newline shape: a newline after every
		// line except possibly the last.
		if i < len(lines)-1 || hasTrailingNewline(path) {
			if _, err := tmp.WriteString("\n"); err != nil {
				cleanup()
				return fmt.Errorf("write %s: %w", tmpName, err)
			}
		}
	}

	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace %s: %w", path, err)
	}

	return syncDir(dir)
}

// hasTrailingNewline reports whether path ends with a newline byte. It re-reads
// only the file's tail, and any error is treated as "yes" so that a file is
// never truncated by accident.
func hasTrailingNewline(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return true
	}
	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, info.Size()-1); err != nil {
		return true
	}
	return buf[0] == '\n'
}

// syncDir fsyncs a directory so that a preceding rename or create survives a
// power loss. Failure is reported because silently losing a backup defeats the
// purpose of making one.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open directory %s: %w", dir, err)
	}
	defer d.Close()

	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync directory %s: %w", dir, err)
	}
	return nil
}
