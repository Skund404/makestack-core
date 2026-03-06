// makestack-core is the headless data management engine for Makestack.
// It reads manifest files from one or more Git data repositories, builds a
// SQLite index, serves data via a REST API, and watches the primary data
// directory for live changes.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/makestack/makestack-core/internal/api"
	"github.com/makestack/makestack-core/internal/federation"
	gitpkg "github.com/makestack/makestack-core/internal/git"
	"github.com/makestack/makestack-core/internal/index"
	"github.com/makestack/makestack-core/internal/parser"
	"github.com/makestack/makestack-core/internal/watcher"
)

func main() {
	dataDir     := flag.String("data", "", "Path to the primary makestack data repository (required)")
	addr        := flag.String("addr", ":8420", "Address to listen on")
	dbPath      := flag.String("db", ":memory:", "SQLite index path (use :memory: for ephemeral)")
	apiKeyFlag  := flag.String("api-key", "", "API key for authentication (overrides MAKESTACK_API_KEY env var)")
	publicReads := flag.Bool("public-reads", false, "Allow unauthenticated access to read-only endpoints")
	flag.Parse()

	if *dataDir == "" {
		log.Fatal("error: -data flag is required")
	}

	// Resolve API key: flag takes precedence over environment variable.
	apiKey := *apiKeyFlag
	if apiKey == "" {
		apiKey = os.Getenv("MAKESTACK_API_KEY")
	}
	if apiKey == "" {
		log.Println("warning: no API key configured — all endpoints are unauthenticated")
	} else if *publicReads {
		log.Println("auth: API key set; read endpoints are public (--public-reads), write endpoints require key")
	} else {
		log.Println("auth: API key set; all endpoints require key")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// — Load federation config ————————————————————————————————————————————————
	// If .makestack/federation.json is absent, a single-root default is returned
	// (backwards-compatible with v0 single-root operation).
	fedConfig, err := federation.LoadConfig(*dataDir)
	if err != nil {
		log.Fatalf("load federation config: %v", err)
	}
	log.Printf("federation: %d root(s) configured", len(fedConfig.Roots))

	// — Load parser configs for all roots ————————————————————————————————————
	parserCfgs := make(map[string]*parser.Config, len(fedConfig.Roots))
	for _, root := range fedConfig.Roots {
		pcPath := filepath.Join(root.Path, root.ParserConfigFile())
		pc, err := parser.LoadConfig(pcPath)
		if err != nil {
			log.Fatalf("load parser config for root %q: %v", root.Slug, err)
		}
		parserCfgs[root.Slug] = pc
	}

	// — Open SQLite index ————————————————————————————————————————————————————
	log.Printf("opening index at %s", *dbPath)
	idx, err := index.Open(*dbPath)
	if err != nil {
		log.Fatalf("open index: %v", err)
	}
	defer idx.Close()

	// — Bulk-load: walk all roots and index their manifests ——————————————————
	var totalIndexed, totalSkipped int
	for _, root := range fedConfig.Roots {
		pc := parserCfgs[root.Slug]
		manifests, err := loadRootManifests(ctx, &root, pc)
		if err != nil {
			log.Fatalf("load root %q: %v", root.Slug, err)
		}
		log.Printf("found %d manifest(s) in root %q (%s)", len(manifests), root.Slug, root.Path)

		for _, m := range manifests {
			pm, err := m.Parse()
			if err != nil {
				log.Printf("warning: skipping %s: %v", m.Path, err)
				totalSkipped++
				continue
			}
			if err := idx.IndexManifest(ctx, pm, root.Slug); err != nil {
				log.Printf("warning: index %s: %v", pm.Path, err)
				totalSkipped++
				continue
			}
			totalIndexed++
		}
	}
	log.Printf("indexed %d primitive(s), skipped %d", totalIndexed, totalSkipped)

	// Rebuild FTS once after the bulk load for a clean, consistent index.
	if err := idx.RebuildFTS(ctx); err != nil {
		log.Printf("warning: rebuild FTS: %v", err)
	}

	// — Start file watcher on primary root only ——————————————————————————————
	// Federated (non-primary) roots are read-only and not watched.
	// Future: scheduled sync for federated roots is out of scope for v0.2.
	primaryRoot := fedConfig.Primary()
	w, err := watcher.New(
		primaryRoot.Path,
		primaryRoot.Slug,
		parserCfgs[primaryRoot.Slug].ManifestFile(),
		idx,
	)
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

	// — Open Git writer for primary root ————————————————————————————————————
	// Non-fatal: read-only endpoints still work without it; write endpoints
	// will return 503 until the primary directory is a valid git repository.
	writer, err := gitpkg.NewWriter(primaryRoot.Path)
	if err != nil {
		log.Printf("warning: git writer unavailable: %v", err)
		writer = nil
	}

	// — Start HTTP server ————————————————————————————————————————————————————
	apiSrv := api.NewServer(idx, writer, apiKey, *publicReads).
		WithFederation(fedConfig, parserCfgs)

	srv := &http.Server{
		Addr:         *addr,
		Handler:      apiSrv,
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

// loadRootManifests walks the directories listed in the root's parser config
// and returns all manifest files found. For non-primary roots, each manifest's
// path is prefixed with the root's slug so it is globally unique in the index.
func loadRootManifests(ctx context.Context, root *federation.Root, cfg *parser.Config) ([]gitpkg.Manifest, error) {
	manifestFile := cfg.ManifestFile()
	var manifests []gitpkg.Manifest

	for dirName := range cfg.Index.Directories {
		dirPath := filepath.Join(root.Path, dirName)
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			continue // directory doesn't exist in this root — skip silently
		}

		err := filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() || d.Name() != manifestFile {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}

			rel, err := filepath.Rel(root.Path, path)
			if err != nil {
				return fmt.Errorf("rel path for %s: %w", path, err)
			}
			rel = filepath.ToSlash(rel)

			// Non-primary roots: prefix path with root slug to ensure global
			// uniqueness in the index. Primary root paths are unchanged for
			// backwards compatibility.
			if !root.Primary {
				rel = root.Slug + "/" + rel
			}

			manifests = append(manifests, gitpkg.Manifest{
				Path: rel,
				Raw:  json.RawMessage(data),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s in root %q: %w", dirName, root.Slug, err)
		}
	}

	return manifests, nil
}
