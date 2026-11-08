// Package atomicfile replaces a file so a reader never observes a partial one.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write replaces path with data: a temporary file in the same directory, Sync,
// rename over path, then fsync of the directory. The result is 0600 and the
// directory is created with 0700 when missing.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	// After a successful rename this removes nothing.
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync() // ENOTSUP on some filesystems is not a write failure
		_ = d.Close()
	}
	return nil
}
