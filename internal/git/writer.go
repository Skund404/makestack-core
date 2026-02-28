// Package git — this file provides write operations on a makestack data
// repository: writing manifest files to disk and committing them via go-git.
package git

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// commitAuthor is the identity used for all commits made by makestack-core.
// In a future auth-aware version this will reflect the authenticated user.
var commitAuthor = &object.Signature{
	Name:  "makestack-core",
	Email: "core@makestack",
}

// Writer writes manifest files to a makestack data directory that is managed
// as a Git repository, staging and committing each change automatically.
type Writer struct {
	dataDir string
	repo    *gogit.Repository
}

// NewWriter opens the Git repository at dataDir. If the directory does not
// yet have a .git folder, it is initialised as a new repository so callers
// never need to run git init manually.
func NewWriter(dataDir string) (*Writer, error) {
	repo, err := gogit.PlainOpen(dataDir)
	if errors.Is(err, gogit.ErrRepositoryNotExists) {
		repo, err = gogit.PlainInit(dataDir, false /* not bare */)
		if err != nil {
			return nil, fmt.Errorf("init git repo at %s: %w", dataDir, err)
		}
		log.Printf("git: initialised new repository at %s", dataDir)
	} else if err != nil {
		return nil, fmt.Errorf("open git repo at %s: %w", dataDir, err)
	}

	return &Writer{dataDir: dataDir, repo: repo}, nil
}

// WriteManifest writes data to relPath (relative to the data directory),
// stages the file, and creates a commit with the given message.
// The parent directory is created automatically if it does not exist.
func (w *Writer) WriteManifest(relPath string, data []byte, commitMsg string) error {
	absPath := filepath.Join(w.dataDir, relPath)

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(absPath), err)
	}
	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", absPath, err)
	}

	wt, err := w.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	// Stage the file. relPath must use forward slashes for go-git on all
	// platforms; filepath.ToSlash handles Windows if ever needed.
	if _, err := wt.Add(filepath.ToSlash(relPath)); err != nil {
		return fmt.Errorf("git add %s: %w", relPath, err)
	}

	if _, err := wt.Commit(commitMsg, &gogit.CommitOptions{
		Author: authorNow(),
	}); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	return nil
}

// DeleteManifest removes relPath from disk and from the Git index, then
// commits. The primitive's parent directory is also removed if it is empty
// after the manifest is deleted (which it will be for any standard primitive).
func (w *Writer) DeleteManifest(relPath string, commitMsg string) error {
	wt, err := w.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	// Remove the file from disk and stage the deletion.
	if _, err := wt.Remove(filepath.ToSlash(relPath)); err != nil {
		return fmt.Errorf("git rm %s: %w", relPath, err)
	}

	// Best-effort removal of the now-empty parent directory.
	// os.Remove silently fails when the directory is not empty.
	parentDir := filepath.Dir(filepath.Join(w.dataDir, relPath))
	os.Remove(parentDir) //nolint:errcheck

	if _, err := wt.Commit(commitMsg, &gogit.CommitOptions{
		Author: authorNow(),
	}); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	return nil
}

// authorNow returns a commit Signature stamped with the current time.
func authorNow() *object.Signature {
	return &object.Signature{
		Name:  commitAuthor.Name,
		Email: commitAuthor.Email,
		When:  time.Now(),
	}
}
