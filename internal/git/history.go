// Package git — this file provides historical manifest reads from specific
// Git commits. The Shell's inventory model stores commit-hash pointers so
// that an inventory record always references the exact version of a catalogue
// entry that existed when the item was added.
package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrNotFound is returned by ReadManifestAtCommit when the commit hash is
// unknown or the file path did not exist at that commit.
var ErrNotFound = errors.New("not found in git repository")

// ReadManifestAtCommit returns the parsed manifest at manifestPath as it
// existed in the given Git commit. The commitHash must be a full 40-character
// SHA-1 hex string (abbreviated hashes are not supported).
//
// Returns ErrNotFound (wrapped) in two cases:
//   - the commit hash does not exist in the repository
//   - the file at manifestPath was not present in that commit's tree
func (w *Writer) ReadManifestAtCommit(manifestPath, commitHash string) (*ParsedManifest, error) {
	hash := plumbing.NewHash(commitHash)

	commit, err := w.repo.CommitObject(hash)
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return nil, fmt.Errorf("%w: commit %s", ErrNotFound, commitHash)
		}
		return nil, fmt.Errorf("resolve commit %s: %w", commitHash, err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("get tree for commit %s: %w", commitHash, err)
	}

	// go-git uses forward-slash paths regardless of host OS.
	file, err := tree.File(filepath.ToSlash(manifestPath))
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, fmt.Errorf("%w: %s at commit %s", ErrNotFound, manifestPath, commitHash)
		}
		return nil, fmt.Errorf("find %s at commit %s: %w", manifestPath, commitHash, err)
	}

	contents, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("read %s at commit %s: %w", manifestPath, commitHash, err)
	}

	m := Manifest{Path: manifestPath, Raw: json.RawMessage(contents)}
	return m.Parse()
}

// HeadHash returns the hash of the current HEAD commit as a 40-character hex
// string. Returns an error if the repository has no commits yet.
func (w *Writer) HeadHash() (string, error) {
	ref, err := w.repo.Head()
	if err != nil {
		return "", fmt.Errorf("resolve HEAD: %w", err)
	}
	return ref.Hash().String(), nil
}
