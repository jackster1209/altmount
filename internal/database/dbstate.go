package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// stateFileName is the app-owned marker recording which backend Altmount last
// successfully ran on / migrated to. It lives next to the config file (the
// durable /config mount in the Docker image) and is written by exactly one
// caller at exactly one moment: the migration commit point, after a verified
// copy. It is intentionally NOT part of config.yaml — config holds the user's
// chosen target (a preference), whereas this marker is runtime history that the
// operator must not be able to edit by hand, since a forgeable marker is not a
// safety net.
const stateFileName = ".altmount_db_state"

// DBState is the on-disk marker describing the last-active database backend.
type DBState struct {
	// Backend is the backend last successfully run on / migrated to:
	// "sqlite" or "postgres".
	Backend string `json:"backend"`
	// SchemaVersion is the goose migration version the backend was last at.
	// Stored so startup can distinguish "same backend, pending schema
	// migrations" from "the backend actually changed".
	SchemaVersion int64 `json:"schema_version"`
}

// StatePath returns the marker path derived from the active config file path.
// Both the serve command and the migrate command resolve --config, so the
// marker always lands in the same durable directory regardless of caller.
func StatePath(configFile string) string {
	dir := filepath.Dir(configFile)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, stateFileName)
}

// ReadState reads the marker. The boolean result reports whether the marker
// exists; a missing marker is not an error (it is the normal first-run state).
func ReadState(path string) (DBState, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DBState{}, false, nil
		}
		return DBState{}, false, fmt.Errorf("read db state %q: %w", path, err)
	}
	var st DBState
	if err := json.Unmarshal(data, &st); err != nil {
		return DBState{}, false, fmt.Errorf("parse db state %q: %w", path, err)
	}
	return st, true, nil
}

// WriteState writes the marker atomically (temp file + rename) so a crash mid
// write can never leave a half-written marker.
func WriteState(path string, st DBState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode db state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write db state temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit db state: %w", err)
	}
	return nil
}

// SchemaVersion returns the highest applied goose migration version for the
// given connection, or 0 if the goose bookkeeping table is absent (a brand-new
// database before its first migration run).
func SchemaVersion(conn *sql.DB, d Dialect) (int64, error) {
	if !hasTable(conn, d, "goose_db_version") {
		return 0, nil
	}
	var v sql.NullInt64
	// version_id selection is identical across dialects.
	err := conn.QueryRow("SELECT MAX(version_id) FROM goose_db_version").Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}
