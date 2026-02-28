// makestack-core is the headless data management engine for Makestack.
// It reads manifest files from a Git data repository, builds a SQLite index,
// serves data via a REST API, and watches the data directory for changes.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/makestack/makestack-core/internal/api"
	gitpkg "github.com/makestack/makestack-core/internal/git"
	"github.com/makestack/makestack-core/internal/index"
	"github.com/makestack/makestack-core/internal/watcher"
)

func main() {
	dataDir := flag.String("data", "", "Path to the makestack data repository (required)")
	addr := flag.String("addr", ":8420", "Address to listen on")
	dbPath := flag.String("db", ":memory:", "SQLite index path (use :memory: for ephemeral)")
	flag.Parse()

	if *dataDir == "" {
		log.Fatal("error: -data flag is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// — Open SQLite index ————————————————————————————————————————————————————
	log.Printf("opening index at %s", *dbPath)
	idx, err := index.Open(*dbPath)
	if err != nil {
		log.Fatalf("open index: %v", err)
	}
	defer idx.Close()

	// — Bulk-load: read all manifests and index them ———————————————————————
	reader, err := gitpkg.NewReader(*dataDir)
	if err != nil {
		log.Fatalf("create reader: %v", err)
	}

	manifests, err := reader.ReadAll(ctx)
	if err != nil {
		log.Fatalf("read manifests: %v", err)
	}
	log.Printf("found %d manifest(s) in %s", len(manifests), *dataDir)

	var indexed, skipped int
	for _, m := range manifests {
		pm, err := m.Parse()
		if err != nil {
			log.Printf("warning: skipping %s: %v", m.Path, err)
			skipped++
			continue
		}
		if err := idx.IndexManifest(ctx, pm); err != nil {
			log.Printf("warning: index %s: %v", pm.Path, err)
			skipped++
			continue
		}
		indexed++
	}
	log.Printf("indexed %d primitive(s), skipped %d", indexed, skipped)

	// Rebuild FTS once after the bulk load for a clean, consistent index.
	if err := idx.RebuildFTS(ctx); err != nil {
		log.Printf("warning: rebuild FTS: %v", err)
	}

	// — Start file watcher ——————————————————————————————————————————————————
	w, err := watcher.New(*dataDir, idx)
	if err != nil {
		// Non-fatal: the server still works without live reloading.
		log.Printf("warning: file watcher unavailable: %v", err)
	} else {
		go func() {
			if err := w.Run(ctx); err != nil {
				log.Printf("watcher: %v", err)
			}
		}()
	}

	// — Open Git writer ———————————————————————————————————————————————————————
	// Non-fatal: read-only endpoints still work without it; write endpoints
	// will return 503 until the data directory is a valid git repository.
	writer, err := gitpkg.NewWriter(*dataDir)
	if err != nil {
		log.Printf("warning: git writer unavailable: %v", err)
		writer = nil
	}

	// — Start HTTP server ————————————————————————————————————————————————————
	srv := &http.Server{
		Addr:         *addr,
		Handler:      api.NewServer(idx, writer),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("makestack-core listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	// Wait for shutdown signal.
	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
