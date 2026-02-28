// makestack-core is the headless data management engine for Makestack.
// It reads manifest files from a Git data repository, builds a SQLite index,
// and serves the data via a REST API.
package main

import (
	"context"
	"encoding/json"
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

	// — Read all manifests from the data directory —————————————————————————
	reader, err := gitpkg.NewReader(*dataDir)
	if err != nil {
		log.Fatalf("create reader: %v", err)
	}

	manifests, err := reader.ReadAll(ctx)
	if err != nil {
		log.Fatalf("read manifests: %v", err)
	}
	log.Printf("found %d manifest(s) in %s", len(manifests), *dataDir)

	// — Parse and index every manifest ————————————————————————————————————
	var indexed, skipped int
	for _, m := range manifests {
		pm, err := m.Parse()
		if err != nil {
			log.Printf("warning: skipping %s: %v", m.Path, err)
			skipped++
			continue
		}

		p := primitiveFromParsed(pm)
		rels := relationshipsFromParsed(pm)

		if err := idx.UpsertFull(ctx, p, rels); err != nil {
			log.Printf("warning: index %s: %v", pm.Path, err)
			skipped++
			continue
		}
		indexed++
	}
	log.Printf("indexed %d primitive(s), skipped %d", indexed, skipped)

	// — Rebuild FTS after bulk insert ————————————————————————————————————
	if err := idx.RebuildFTS(ctx); err != nil {
		log.Printf("warning: rebuild FTS: %v", err)
	}

	// — Start HTTP server ————————————————————————————————————————————————
	srv := &http.Server{
		Addr:         *addr,
		Handler:      api.NewServer(idx),
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

// primitiveFromParsed converts a git.ParsedManifest to the index.Primitive
// type expected by the indexer.
func primitiveFromParsed(pm *gitpkg.ParsedManifest) index.Primitive {
	p := index.Primitive{
		ID:            pm.ID,
		Type:          pm.Type,
		Name:          pm.Name,
		Slug:          pm.Slug,
		Path:          pm.Path,
		Created:       pm.Created,
		Modified:      pm.Modified,
		Description:   pm.Description,
		ClonedFrom:    pm.ClonedFrom,
		ParentProject: pm.ParentProject,
		Properties:    pm.Properties,
		Manifest:      pm.Raw,
	}

	// Encode the []string tags slice as a JSON array for the index.
	if len(pm.Tags) > 0 {
		if b, err := json.Marshal(pm.Tags); err == nil {
			p.Tags = json.RawMessage(b)
		}
	} else {
		p.Tags = json.RawMessage("[]")
	}

	return p
}

// relationshipsFromParsed converts the relationships embedded in a parsed
// manifest into the flat index.Relationship rows the indexer expects.
func relationshipsFromParsed(pm *gitpkg.ParsedManifest) []index.Relationship {
	if len(pm.Relationships) == 0 {
		return nil
	}
	rels := make([]index.Relationship, len(pm.Relationships))
	for i, r := range pm.Relationships {
		rels[i] = index.Relationship{
			SourcePath: pm.Path,
			SourceType: pm.Type,
			RelType:    r.Type,
			TargetPath: r.Target,
			Metadata:   r.Metadata,
		}
	}
	return rels
}
