// Package git — this file provides historical manifest reads from specific
// Git commits. The Shell's inventory model stores commit-hash pointers so
// that an inventory record always references the exact version of a catalogue
// entry that existed when the item was added. It also provides commit history
// and structured diff utilities used by the /history and /diff endpoints.
package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	gogit "github.com/go-git/go-git/v5"
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

// LastCommitHashForPath returns the hash of the most recent commit that
// touched manifestPath as a 40-character hex string. This is the correct hash
// for the Shell's inventory pointer model: it identifies the exact version of
// a catalogue entry, not merely the latest commit in the repository.
//
// Returns ErrNotFound (wrapped) when no commit has ever touched manifestPath.
func (w *Writer) LastCommitHashForPath(manifestPath string) (string, error) {
	commits, _, err := w.CommitHistoryForPath(manifestPath, 1, 0)
	if err != nil {
		return "", err
	}
	if len(commits) == 0 {
		return "", fmt.Errorf("%w: no commits found for path %s", ErrNotFound, manifestPath)
	}
	return commits[0].Hash, nil
}

// CommitInfo holds the metadata for a single Git commit, used by the
// /history endpoint to describe each version of a primitive.
type CommitInfo struct {
	Hash      string `json:"hash"`
	Message   string `json:"message"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
}

// CommitHistoryForPath returns the commits that touched manifestPath, newest
// first. limit and offset control pagination; total is the full count across
// all pages. Returns an empty slice (not an error) when no commits match.
func (w *Writer) CommitHistoryForPath(manifestPath string, limit, offset int) ([]CommitInfo, int, error) {
	slashPath := filepath.ToSlash(manifestPath)

	cIter, err := w.repo.Log(&gogit.LogOptions{
		PathFilter: func(p string) bool { return p == slashPath },
	})
	if err != nil {
		return nil, 0, fmt.Errorf("git log for %s: %w", manifestPath, err)
	}

	var all []CommitInfo
	err = cIter.ForEach(func(c *object.Commit) error {
		all = append(all, CommitInfo{
			Hash:      c.Hash.String(),
			Message:   c.Message,
			Author:    c.Author.Name,
			Timestamp: c.Author.When.UTC().Format(time.RFC3339),
		})
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("iterate commits for %s: %w", manifestPath, err)
	}

	total := len(all)
	if offset >= total {
		return []CommitInfo{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

// CommitTimestamp returns the author timestamp of the given commit as an
// RFC3339 string. Returns ErrNotFound (wrapped) if the commit does not exist.
func (w *Writer) CommitTimestamp(commitHash string) (string, error) {
	commit, err := w.repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return "", fmt.Errorf("%w: commit %s", ErrNotFound, commitHash)
		}
		return "", fmt.Errorf("resolve commit %s: %w", commitHash, err)
	}
	return commit.Author.When.UTC().Format(time.RFC3339), nil
}

// ParentHash returns the hash of the first parent of the given commit.
// Returns ErrNotFound (wrapped) if the commit is the initial commit (no parents).
func (w *Writer) ParentHash(commitHash string) (string, error) {
	commit, err := w.repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return "", fmt.Errorf("%w: commit %s", ErrNotFound, commitHash)
		}
		return "", fmt.Errorf("resolve commit %s: %w", commitHash, err)
	}
	if len(commit.ParentHashes) == 0 {
		return "", fmt.Errorf("%w: commit %s has no parent", ErrNotFound, commitHash)
	}
	return commit.ParentHashes[0].String(), nil
}

// FieldChange describes a single field-level difference between two versions
// of a manifest. It is used by the /diff endpoint.
type FieldChange struct {
	Field    string      `json:"field"`
	Type     string      `json:"type"` // "added", "removed", or "modified"
	OldValue interface{} `json:"old_value,omitempty"`
	NewValue interface{} `json:"new_value,omitempty"`
}

// DiffManifests computes a structured field-level diff between two raw JSON
// manifest documents. Changes are sorted by field path for stable output.
// Returns nil if either document cannot be parsed as a JSON object.
func DiffManifests(from, to json.RawMessage) []FieldChange {
	var fromMap, toMap map[string]interface{}
	if err := json.Unmarshal(from, &fromMap); err != nil {
		return nil
	}
	if err := json.Unmarshal(to, &toMap); err != nil {
		return nil
	}

	var changes []FieldChange
	diffMaps("", fromMap, toMap, &changes)

	// Sort by field path for deterministic output.
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Field < changes[j].Field
	})
	return changes
}

// diffMaps collects field-level differences between two JSON objects.
func diffMaps(prefix string, from, to map[string]interface{}, out *[]FieldChange) {
	for k, fromVal := range from {
		field := joinPath(prefix, k)
		if toVal, ok := to[k]; !ok {
			*out = append(*out, FieldChange{Field: field, Type: "removed", OldValue: fromVal})
		} else {
			diffValues(field, fromVal, toVal, out)
		}
	}
	for k, toVal := range to {
		if _, ok := from[k]; !ok {
			field := joinPath(prefix, k)
			*out = append(*out, FieldChange{Field: field, Type: "added", NewValue: toVal})
		}
	}
}

// diffValues recurses into maps and slices, or records a scalar change.
func diffValues(field string, from, to interface{}, out *[]FieldChange) {
	fromMap, fromIsMap := from.(map[string]interface{})
	toMap, toIsMap := to.(map[string]interface{})
	if fromIsMap && toIsMap {
		diffMaps(field, fromMap, toMap, out)
		return
	}

	fromSlice, fromIsSlice := from.([]interface{})
	toSlice, toIsSlice := to.([]interface{})
	if fromIsSlice && toIsSlice {
		diffSlices(field, fromSlice, toSlice, out)
		return
	}

	// Scalar comparison (or type change).
	if !jsonEqual(from, to) {
		*out = append(*out, FieldChange{Field: field, Type: "modified", OldValue: from, NewValue: to})
	}
}

// diffSlices compares two JSON arrays element-by-element.
func diffSlices(prefix string, from, to []interface{}, out *[]FieldChange) {
	minLen := len(from)
	if len(to) < minLen {
		minLen = len(to)
	}
	for i := 0; i < minLen; i++ {
		diffValues(fmt.Sprintf("%s[%d]", prefix, i), from[i], to[i], out)
	}
	for i := minLen; i < len(from); i++ {
		*out = append(*out, FieldChange{
			Field:    fmt.Sprintf("%s[%d]", prefix, i),
			Type:     "removed",
			OldValue: from[i],
		})
	}
	for i := minLen; i < len(to); i++ {
		*out = append(*out, FieldChange{
			Field:    fmt.Sprintf("%s[%d]", prefix, i),
			Type:     "added",
			NewValue: to[i],
		})
	}
}

// joinPath concatenates a JSON path prefix and a key.
func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// jsonEqual reports whether two interface{} values are equal when marshalled
// to JSON. Used to compare scalar values across different numeric types.
func jsonEqual(a, b interface{}) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}
