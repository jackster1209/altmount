// Package testdb provides a single factory for building a fully-migrated
// *database.DB on either backend, so the same test can run against SQLite and
// PostgreSQL. It is the foundation of the dual-engine CI matrix: tests call
// testdb.New(t) instead of opening a hard-coded sqlite :memory: connection, and
// the engine is chosen by environment so CI can exercise both.
//
// Engine selection:
//
//	ALTMOUNT_TEST_DB      "sqlite" (default) | "postgres"
//	ALTMOUNT_TEST_PG_DSN  base DSN of a Postgres server, used only when
//	                      ALTMOUNT_TEST_DB=postgres. Each test gets its own
//	                      throwaway schema for isolation; if this is unset the
//	                      Postgres path skips rather than fails.
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/javi11/altmount/internal/database"
)

// Engine returns the backend the suite is configured to run against.
func Engine() string {
	if e := os.Getenv("ALTMOUNT_TEST_DB"); e != "" {
		return e
	}
	return "sqlite"
}

// New returns a migrated database for the active engine. Cleanup (closing the
// connection and, for Postgres, dropping the per-test schema) is registered via
// t.Cleanup, so callers never have to tear anything down.
func New(t *testing.T) *database.DB {
	t.Helper()
	switch Engine() {
	case "postgres":
		return newPostgres(t)
	case "sqlite", "":
		return newSQLite(t)
	default:
		t.Fatalf("testdb: unknown ALTMOUNT_TEST_DB=%q (want sqlite|postgres)", Engine())
		return nil
	}
}

func newSQLite(t *testing.T) *database.DB {
	t.Helper()
	// A real temp file (not :memory:) so the connection pool and goose
	// migrations behave exactly as they do in production.
	path := t.TempDir() + "/altmount-test.db"
	db, err := database.NewDB(database.Config{Type: "sqlite", DatabasePath: path})
	if err != nil {
		t.Fatalf("testdb: open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newPostgres(t *testing.T) *database.DB {
	t.Helper()
	base := os.Getenv("ALTMOUNT_TEST_PG_DSN")
	if base == "" {
		t.Skip("testdb: ALTMOUNT_TEST_PG_DSN not set; skipping Postgres leg")
	}

	schema := "test_" + randID(t)

	// Bootstrap connection: create an isolated schema for this test.
	boot, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("testdb: bootstrap open: %v", err)
	}
	if _, err := boot.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		_ = boot.Close()
		t.Fatalf("testdb: create schema %s: %v", schema, err)
	}
	_ = boot.Close()

	// Point every connection in the pool at the per-test schema so goose's
	// version table and all migrated objects land there in isolation.
	dsn, err := withSearchPath(base, schema)
	if err != nil {
		t.Fatalf("testdb: build dsn: %v", err)
	}

	db, err := database.NewDB(database.Config{Type: "postgres", DSN: dsn})
	if err != nil {
		dropSchema(t, base, schema)
		t.Fatalf("testdb: open postgres (migrations): %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
		dropSchema(t, base, schema)
	})
	return db
}

// withSearchPath injects `options=-c search_path=<schema>` into a Postgres URL
// DSN so the connection startup sets the schema before any query runs.
func withSearchPath(base, schema string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("options", "-c search_path="+schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func dropSchema(t *testing.T, base, schema string) {
	t.Helper()
	boot, err := sql.Open("pgx", base)
	if err != nil {
		t.Logf("testdb: cleanup open failed: %v", err)
		return
	}
	defer func() { _ = boot.Close() }()
	if _, err := boot.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema)); err != nil {
		t.Logf("testdb: drop schema %s failed: %v", schema, err)
	}
}

func randID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("testdb: rand: %v", err)
	}
	return hex.EncodeToString(b)
}
