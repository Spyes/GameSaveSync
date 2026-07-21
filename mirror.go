package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// mirror makes dst an exact copy of src: it copies every file/dir from src,
// and deletes anything in dst that is not present in src. It is the moral
// equivalent of `rsync -a --delete src/ dst/`.
//
// It never touches paths named ".git" so a mirror into a clone's subfolder
// can't clobber repository metadata.
func mirror(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	// Build the set of relative paths that should exist in dst.
	want := map[string]bool{}
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if info.Name() == ".git" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		want[rel] = true
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target, info)
	})
	if err != nil {
		return fmt.Errorf("copying %s -> %s: %w", src, dst, err)
	}

	// Delete anything in dst that src no longer has.
	return prune(dst, want)
}

// prune removes files and dirs under dst whose relative path is not in want.
func prune(dst string, want map[string]bool) error {
	// Collect first, delete after, so we don't mutate the tree mid-walk.
	var toRemove []string
	err := filepath.Walk(dst, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dst, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if info.Name() == ".git" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !want[rel] {
			toRemove = append(toRemove, path)
			if info.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, path := range toRemove {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("pruning %s: %w", path, err)
		}
	}
	return nil
}

func copyFile(src, dst string, info os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
