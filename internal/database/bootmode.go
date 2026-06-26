package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BootMode describes what action to take at startup.
type BootMode int

const (
	// BootModeNormal means the configured backend matches the last active backend
	// (or this is a fresh install with no data to migrate). Normal startup proceeds.
	BootModeNormal BootMode = iota
	// BootModeMaintenance means the configured backend differs from the last active
	// backend, or an existing populated SQLite file was found when config targets
	// postgres. The server must boot into maintenance mode and complete the data
	// migration before normal operation can resume.
	BootModeMaintenance
)

// BootDecision is the result of the startup backend detection.
type BootDecision struct {
	Mode       BootMode
	SourceType string // "sqlite" or "postgres"
	TargetType string // "sqlite" or "postgres"
	Reason     string
}

// DetectBootMode reads the marker and config to determine whether normal or
// maintenance boot is needed. It implements the decision tree from the database
// backend switching design:
//
//  1. Marker present and backend matches config → normal boot.
//  2. Marker present and backend mismatches config → maintenance (migration needed).
//  3. Marker absent, config targets postgres, existing SQLite has rows → maintenance
//     (existing user upgrading to this feature for the first time).
//  4. All other cases → normal boot (fresh install or same backend, no marker yet).
func DetectBootMode(configType, sqlitePath, markerPath string) (BootDecision, error) {
	if configType == "" {
		configType = "sqlite"
	}

	state, exists, err := ReadState(markerPath)
	if err != nil {
		return BootDecision{}, fmt.Errorf("read marker: %w", err)
	}

	if exists {
		if state.Backend == configType {
			return BootDecision{
				Mode:       BootModeNormal,
				TargetType: configType,
				Reason:     "marker matches configured backend",
			}, nil
		}
		return BootDecision{
			Mode:       BootModeMaintenance,
			SourceType: state.Backend,
			TargetType: configType,
			Reason:     fmt.Sprintf("backend switched from %s to %s", state.Backend, configType),
		}, nil
	}

	// No marker yet. Check for the "existing SQLite user upgrading to postgres" case.
	if configType == "postgres" && hasSQLiteData(sqlitePath) {
		return BootDecision{
			Mode:       BootModeMaintenance,
			SourceType: "sqlite",
			TargetType: "postgres",
			Reason:     "existing SQLite data detected; migration to postgres required",
		}, nil
	}

	// Fresh install or marker-less SQLite-to-SQLite: normal boot.
	return BootDecision{
		Mode:       BootModeNormal,
		TargetType: configType,
		Reason:     "no migration needed",
	}, nil
}

// PingDB validates that the database endpoint described by cfg is reachable
// without running schema migrations. For SQLite it checks that the parent
// directory exists and is accessible; for Postgres it opens the connection and
// pings with a 5-second timeout.
func PingDB(ctx context.Context, cfg Config) error {
	switch cfg.Type {
	case "postgres":
		if cfg.DSN == "" {
			return fmt.Errorf("postgres DSN is required")
		}
		conn, err := sql.Open("pgx", cfg.DSN)
		if err != nil {
			return fmt.Errorf("open postgres connection: %w", err)
		}
		defer conn.Close()
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := conn.PingContext(pingCtx); err != nil {
			return fmt.Errorf("ping postgres: %w", err)
		}
		return nil
	default:
		if cfg.DatabasePath == "" {
			return fmt.Errorf("sqlite database path is required")
		}
		dir := filepath.Dir(cfg.DatabasePath)
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("sqlite directory not accessible: %w", err)
		}
		return nil
	}
}

// hasSQLiteData returns true when the SQLite file at path exists and contains at
// least one user-data row across the key application tables. Checking a spread
// of tables is necessary because import_queue can be empty for an active user
// whose imports have all completed, while their real library lives in media_files,
// file_health, and import_history. Each table is queried independently so that a
// missing table (pre-migration database) is treated as zero rows rather than an
// error. A missing file, unreadable file, or a file with no rows in any checked
// table returns false (treated as empty / fresh install).
func hasSQLiteData(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	// Open read-only — never create the file.
	conn, err := sql.Open("sqlite3", path+"?mode=ro")
	if err != nil {
		return false
	}
	defer conn.Close()

	// Check the tables most likely to contain durable user data. import_queue
	// items are deleted after processing, so it alone is not a reliable signal.
	tables := []string{"import_queue", "media_files", "file_health", "import_history"}
	for _, table := range tables {
		var count int64
		if err := conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			continue // table may not exist yet on a pre-migration database
		}
		if count > 0 {
			return true
		}
	}
	return false
}
