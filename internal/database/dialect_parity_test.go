package database_test

// Dialect-parity pack.
//
// These tests run under the dual-engine CI matrix (see .github/workflows/
// db-compat.yml). On the SQLite leg they pass today and pin the expected
// behavior; on the Postgres leg they prove the dialect fixes are correct.
//
// Tests that depend on a not-yet-merged fix Skip on Postgres with an explicit
// phase reference. As each phase lands, delete the Skip and the Postgres leg
// starts enforcing that fix forever. This is the lever that keeps hand-written
// dual-dialect SQL maintainable: a query that's wrong on one engine fails CI.

import (
	"context"
	"testing"

	"github.com/javi11/altmount/internal/database"
	"github.com/javi11/altmount/internal/database/testdb"
)

// TestParity_Smoke proves the harness end-to-end: migrations apply and basic
// queries run on whichever engine the matrix selected. Green on both today
// (after the Phase 1 schema fixes), so it guards the build harness itself.
func TestParity_Smoke(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	health := database.NewHealthRepository(db.Connection(), db.Dialect())
	if _, err := health.GetHealthStats(ctx); err != nil {
		t.Fatalf("GetHealthStats on %s: %v", testdb.Engine(), err)
	}

	repo := database.NewRepository(db.Connection(), db.Dialect())
	if _, err := repo.GetQueueStats(ctx); err != nil {
		t.Fatalf("GetQueueStats on %s: %v", testdb.Engine(), err)
	}
}

// TestParity_ProviderHourlyUpsertAndWindow exercises two dialect-sensitive
// surfaces at once:
//   - the ON CONFLICT ... bytes_downloaded self-reference upsert (Class 3), and
//   - the datetime('now','-N hours') window inside GetProviderHourlyStats (Class 2).
//
// Writing the same provider twice must accumulate, and the read-back window must
// return that sum. Identical expectation on both engines.
func TestParity_ProviderHourlyUpsertAndWindow(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	repo := database.NewRepository(db.Connection(), db.Dialect())

	const provider = "news.example.com:563"
	if err := repo.AddProviderBytesToHourlyStat(ctx, provider, 1_000); err != nil {
		t.Fatalf("first upsert on %s: %v", testdb.Engine(), err)
	}
	if err := repo.AddProviderBytesToHourlyStat(ctx, provider, 2_500); err != nil {
		t.Fatalf("second upsert on %s: %v", testdb.Engine(), err)
	}

	stats, err := repo.GetProviderHourlyStats(ctx, 1)
	if err != nil {
		t.Fatalf("GetProviderHourlyStats on %s: %v", testdb.Engine(), err)
	}
	if got := stats[provider]; got != 3_500 {
		t.Fatalf("provider hourly bytes on %s = %d, want 3500", testdb.Engine(), got)
	}
}

// TestParity_IndexerHealthStats covers the getIndexerHealthStats query, whose
// `datetime('now','-24 hours')` reaches Postgres through q() (the package-level
// function holds no dialectHelper to branch on). The query must parse and run on
// both engines even with no rows — on Postgres pre-fix it failed at plan time
// with "function datetime(unknown, unknown) does not exist".
func TestParity_IndexerHealthStats(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	repo := database.NewRepository(db.Connection(), db.Dialect())

	if _, err := repo.GetIndexerHealthStats(ctx); err != nil {
		t.Fatalf("GetIndexerHealthStats on %s: %v", testdb.Engine(), err)
	}
}

// TestParity_ListHealthRecords covers the file-health listing and count queries,
// whose optional status/since filters previously emitted `? IS NULL` — an
// untyped bare parameter that Postgres rejects with 42P18 ("could not determine
// data type of parameter"). Must run with and without filters on both engines.
func TestParity_ListHealthRecords(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	repo := database.NewHealthRepository(db.Connection(), db.Dialect())

	// No filters — the path that previously failed on Postgres.
	if _, err := repo.ListHealthItems(ctx, nil, 50, 0, nil, "", "", ""); err != nil {
		t.Fatalf("ListHealthItems(no filters) on %s: %v", testdb.Engine(), err)
	}
	if _, err := repo.CountHealthItems(ctx, nil, nil, ""); err != nil {
		t.Fatalf("CountHealthItems(no filters) on %s: %v", testdb.Engine(), err)
	}

	// With a status filter and a search term (the typed paths).
	status := database.HealthStatusPending
	if _, err := repo.ListHealthItems(ctx, &status, 50, 0, nil, "movie", "created_at", "desc"); err != nil {
		t.Fatalf("ListHealthItems(status+search) on %s: %v", testdb.Engine(), err)
	}
}
