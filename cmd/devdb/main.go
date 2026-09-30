// Command devdb runs a local PostgreSQL 17 for development, without root: the
// binaries download once into ~/.embedded-postgres-go and data persists in
// build/pgdata. It prints the DATABASE_URL to use and runs until Ctrl-C.
//
//	go run ./cmd/devdb            # port 5433
//	go run ./cmd/devdb -reset     # wipe build/pgdata first
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

func main() {
	port := flag.Uint("port", 5433, "port to listen on")
	dir := flag.String("dir", "build/pgdata", "data directory")
	reset := flag.Bool("reset", false, "delete the data directory first")
	flag.Parse()

	abs, err := filepath.Abs(*dir)
	if err != nil {
		fail(err)
	}
	if *reset {
		if err := os.RemoveAll(abs); err != nil {
			fail(err)
		}
	}
	db := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V17).Port(uint32(*port)).
		Database("site").Username("site").Password("site").
		DataPath(filepath.Join(abs, "data")).RuntimePath(filepath.Join(abs, "run")))
	if err := db.Start(); err != nil {
		fail(err)
	}
	fmt.Printf("\nPostgres 17 ready. Use:\n  export DATABASE_URL='postgres://site:site@localhost:%d/site?sslmode=disable'\n\n", *port)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	if err := db.Stop(); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "devdb:", err)
	os.Exit(1)
}
