// makestack-core is the headless data management engine for Makestack.
// It manages JSON files in a Git repository, maintains a SQLite read index,
// and serves data via REST API.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dataDir := flag.String("data", "", "Path to the makestack data repository (required)")
	addr := flag.String("addr", ":8420", "Address to listen on")
	flag.Parse()

	if *dataDir == "" {
		log.Fatal("error: -data flag is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("makestack-core starting on %s, data dir: %s", *addr, *dataDir)

	// TODO: initialize index, API server, and file watcher

	<-ctx.Done()
	log.Println("shutting down")
}
