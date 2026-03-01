// Package watcher monitors the makestack data directory for changes to
// manifest.json files and incrementally updates the SQLite index so the
// running server always reflects the current state of the data directory
// without needing to restart.
package watcher

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	gitpkg "github.com/makestack/makestack-core/internal/git"
	"github.com/makestack/makestack-core/internal/index"
)

// debounceDelay is how long we wait after the last file-system event on a
// path before acting. Editors often emit several events per logical save
// (e.g. truncate, write, chmod, or write-to-temp then rename-into-place).
// 200 ms is enough for the file to settle while still feeling instant.
const debounceDelay = 200 * time.Millisecond

// pendingEntry tracks the debounce timer for one path.
type pendingEntry struct {
	timer *time.Timer
}

// Watcher monitors a makestack data directory for manifest.json changes
// and keeps the SQLite index in sync incrementally.
type Watcher struct {
	dataDir string
	idx     *index.Index
	fw      *fsnotify.Watcher

	mu      sync.Mutex
	pending map[string]*pendingEntry // keyed by absolute path
}

// New creates a Watcher for the given data directory and index.
// It immediately registers watches on every existing subdirectory so that
// events from any depth are captured from the moment Run is called.
func New(dataDir string, idx *index.Index) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}

	w := &Watcher{
		dataDir: dataDir,
		idx:     idx,
		fw:      fw,
		pending: make(map[string]*pendingEntry),
	}

	if err := w.watchAllDirs(); err != nil {
		fw.Close()
		return nil, fmt.Errorf("watch dirs under %s: %w", dataDir, err)
	}

	return w, nil
}

// Run processes file-system events until ctx is cancelled.
// It blocks; launch it in a goroutine.
func (w *Watcher) Run(ctx context.Context) error {
	defer w.fw.Close()
	log.Printf("watcher: watching %s", w.dataDir)

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-w.fw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ctx, event)

		case err, ok := <-w.fw.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher: fsnotify error: %v", err)
		}
	}
}

// watchAllDirs adds every directory under dataDir to the fsnotify watcher.
// New directories created at runtime are added in handleEvent.
func (w *Watcher) watchAllDirs() error {
	return filepath.WalkDir(w.dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return w.fw.Add(path)
		}
		return nil
	})
}

// handleEvent is called for every raw fsnotify event.
func (w *Watcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	// When a new directory is created, recursively watch it and schedule
	// debounced processing for any manifest.json files already inside it.
	// The write API creates the entire directory tree in one shot (MkdirAll +
	// WriteFile), so the directory Create event may fire before WriteFile
	// finishes. Scheduling through the debounce (rather than calling process
	// directly) means we wait for the file to settle — and if the file's own
	// Create event arrives moments later it simply resets the timer, so process
	// runs exactly once on the completed file.
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = filepath.WalkDir(event.Name, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // skip unreadable entries
				}
				if d.IsDir() {
					if addErr := w.fw.Add(path); addErr != nil {
						log.Printf("watcher: watch new dir %s: %v", path, addErr)
					}
				} else if filepath.Base(path) == "manifest.json" {
					w.schedule(ctx, path) // was: w.process(ctx, path)
				}
				return nil
			})
			return
		}
	}

	// Only care about manifest.json files; ignore everything else.
	if filepath.Base(event.Name) != "manifest.json" {
		return
	}

	w.schedule(ctx, event.Name)
}

// schedule arms (or resets) the debounce timer for absPath. If a timer is
// already pending for this path it is cancelled and replaced so that only one
// process call runs — after the file has been quiet for debounceDelay.
// Safe to call from multiple goroutines.
func (w *Watcher) schedule(ctx context.Context, absPath string) {
	w.mu.Lock()
	if e, ok := w.pending[absPath]; ok {
		e.timer.Stop()
	}
	w.pending[absPath] = &pendingEntry{
		timer: time.AfterFunc(debounceDelay, func() {
			w.mu.Lock()
			delete(w.pending, absPath)
			w.mu.Unlock()

			// By the time the timer fires, ctx might be cancelled.
			if ctx.Err() != nil {
				return
			}
			w.process(ctx, absPath)
		}),
	}
	w.mu.Unlock()
}

// process is called after the debounce delay settles. It checks whether the
// file still exists and either upserts or removes it from the index.
func (w *Watcher) process(ctx context.Context, absPath string) {
	rel, err := filepath.Rel(w.dataDir, absPath)
	if err != nil {
		log.Printf("watcher: rel path for %s: %v", absPath, err)
		return
	}

	data, err := os.ReadFile(absPath)
	if os.IsNotExist(err) {
		// File was deleted or moved away — remove it from the index.
		w.removeFromIndex(ctx, rel)
		return
	}
	if err != nil {
		log.Printf("watcher: read %s: %v", rel, err)
		return
	}

	// File exists — parse and upsert.
	w.upsertToIndex(ctx, rel, data)
}

// upsertToIndex parses raw manifest JSON and updates the index.
func (w *Watcher) upsertToIndex(ctx context.Context, rel string, data []byte) {
	m := gitpkg.Manifest{Path: rel, Raw: data}
	pm, err := m.Parse()
	if err != nil {
		log.Printf("watcher: parse %s: %v", rel, err)
		return
	}

	if err := w.idx.IndexManifest(ctx, pm); err != nil {
		log.Printf("watcher: index %s: %v", rel, err)
		return
	}
	if err := w.idx.RebuildFTS(ctx); err != nil {
		log.Printf("watcher: rebuild FTS after upsert of %s: %v", rel, err)
	}
	log.Printf("watcher: indexed %s (%s: %s)", rel, pm.Type, pm.Name)
}

// removeFromIndex deletes a primitive and its relationships from the index.
func (w *Watcher) removeFromIndex(ctx context.Context, rel string) {
	if err := w.idx.Delete(ctx, rel); err != nil {
		log.Printf("watcher: delete %s: %v", rel, err)
		return
	}
	if err := w.idx.RebuildFTS(ctx); err != nil {
		log.Printf("watcher: rebuild FTS after delete of %s: %v", rel, err)
	}
	log.Printf("watcher: removed %s from index", rel)
}
