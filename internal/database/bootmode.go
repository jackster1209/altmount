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
	// BootModeMaintenance means the marker records a different backend than what is
	// configured. The server must boot into maintenance mode and complete the data
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
//  3. No marker → normal boot. The marker is written by serve.go after every
//     successful boot, so the next restart will detect any backend change correctly.
//     This covers first-run and first-run-with-new-image scenarios without
//     requiring user intervention.
func DetectBootMode(configType, _ /* sqlitePath */, markerPath string) (BootDecision, error) {
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

	// No marker: first run or first run with this image version. Boot normally
	// and let serve.go write the marker after successful initialization.
	return BootDecision{
		Mode:       BootModeNormal,
		TargetType: configType,
		Reason:     "no migration needed",
	}, nil
}

// DBConnectionStatus is the outcome of a user-initiated test-connection request.
type DBConnectionStatus struct {
	Status  string // "ok" | "new" | "error"
	Message string
}

// TestDBConnection probes a database endpoint and returns user-facing feedback.
// Unlike PingDB (used for startup validation), this function distinguishes three
// SQLite states: existing file → "ok", valid directory but no file → "new" (will
// be created on restart), inaccessible path → "error". Postgres returns only
// "ok" or "error".
func TestDBConnection(ctx context.Context, cfg Config) DBConnectionStatus {
	switch cfg.Type {
	case "postgres":
		if cfg.DSN == "" {
			return DBConnectionStatus{Status: "error", Message: "postgres DSN is required"}
		}
		conn, err := sql.Open("pgx", cfg.DSN)
		if err != nil {
			return DBConnectionStatus{Status: "error", Message: "open connection: " + err.Error()}
		}
		defer conn.Close()
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := conn.PingContext(pingCtx); err != nil {
			return DBConnectionStatus{Status: "error", Message: "ping failed: " + err.Error()}
		}
		return DBConnectionStatus{Status: "ok", Message: "connection successful"}
	default: // sqlite
		if cfg.DatabasePath == "" {
			return DBConnectionStatus{Status: "error", Message: "database path is required"}
		}
		dir := filepath.Dir(cfg.DatabasePath)
		if _, err := os.Stat(dir); err != nil {
			return DBConnectionStatus{Status: "error", Message: "directory not accessible: " + dir}
		}
		if _, err := os.Stat(cfg.DatabasePath); os.IsNotExist(err) {
			return DBConnectionStatus{
				Status:  "new",
				Message: "no database found at this path — a new one will be created on restart",
			}
		} else if err != nil {
			return DBConnectionStatus{Status: "error", Message: "cannot access file: " + err.Error()}
		}
		return DBConnectionStatus{Status: "ok", Message: "database file found"}
	}
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

