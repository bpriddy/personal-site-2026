package store

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"
)

// One embedded Postgres serves the whole package; each test gets its own
// database, so every test also exercises the migrations from scratch.
// Skipped with -short. First run downloads Postgres into ~/.embedded-postgres-go.
var pgAdminURL string

func TestMain(m *testing.M) {
	flag.Parse()
	code := func() int {
		if testing.Short() || os.Getenv("STORE_SKIP_POSTGRES") != "" {
			return m.Run()
		}
		dir, err := os.MkdirTemp("", "store-pg-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		port := freePort()
		db := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V17).Port(port).
			RuntimePath(dir + "/run").DataPath(dir + "/data").
			Logger(nil))
		if err := db.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "embedded postgres:", err)
			return 1
		}
		defer db.Stop()
		pgAdminURL = fmt.Sprintf("postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", port)
		return m.Run()
	}()
	os.Exit(code)
}

func freePort() uint32 {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port)
}

var dbSeq atomic.Int64

// freshDB creates an empty database and returns its URL.
func freshDB(t *testing.T) string {
	t.Helper()
	if pgAdminURL == "" {
		t.Skip("postgres tests skipped (-short or STORE_SKIP_POSTGRES)")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgAdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	name := fmt.Sprintf("t%d", dbSeq.Add(1))
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("postgres://postgres:postgres@%s/%s?sslmode=disable", conn.Config().Host+":"+fmt.Sprint(conn.Config().Port), name)
}

func openPG(t *testing.T, url string) *Postgres {
	t.Helper()
	p, err := OpenPostgres(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestPostgresConformance(t *testing.T) {
	conformance(t, func(t *testing.T) Store { return openPG(t, freshDB(t)) })
}

func TestMigrateIdempotentAndConcurrent(t *testing.T) {
	url := freshDB(t)
	// several instances starting at once (Cloud Run scale-out) must not race
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			p, err := OpenPostgres(context.Background(), url)
			if err == nil {
				p.Close()
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	p := openPG(t, url) // and once more, sequentially
	migs, _ := loadMigrations()
	var n, maxV int
	if err := p.pool.QueryRow(context.Background(),
		`SELECT count(*), max(version) FROM schema_migrations`).Scan(&n, &maxV); err != nil {
		t.Fatal(err)
	}
	if n != len(migs) || maxV != migs[len(migs)-1].version {
		t.Fatalf("schema_migrations: %d rows, max %d; want %d, %d", n, maxV, len(migs), migs[len(migs)-1].version)
	}
	// seed rows were inserted exactly once
	var pages int
	p.pool.QueryRow(context.Background(), `SELECT count(*) FROM pages`).Scan(&pages)
	if pages != 1 {
		t.Fatalf("pages = %d after repeated migrations, want 1", pages)
	}
}
