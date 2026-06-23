package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// appCopyOrder lists the application tables in the order rows must be copied so
// that foreign keys are satisfied during the copy (parents before children).
// The only FK among these is media_files.nzb_id -> import_queue(id), so
// import_queue must precede media_files. Every other table is independent.
//
// goose_db_version is deliberately absent: the target's schema (and its goose
// bookkeeping) is rebuilt by running migrations during open, never copied.
var appCopyOrder = []string{
	"users",
	"import_queue",
	"media_files",
	"file_health",
	"import_history",
	"import_migrations",
	"import_daily_stats",
	"import_hourly_stats",
	"provider_hourly_stats",
	"provider_speed_tests_history",
	"indexer_import_stats",
	"queue_stats",
	"system_state",
	"system_stats",
}

// MigrateReport summarizes a completed migration for display/logging.
type MigrateReport struct {
	SourceBackend string
	TargetBackend string
	RowsPerTable  map[string]int64
	TotalRows     int64
}

// ProgressFunc receives human-readable progress lines. May be nil.
type ProgressFunc func(string)

func (p ProgressFunc) emit(format string, args ...any) {
	if p != nil {
		p(fmt.Sprintf(format, args...))
	}
}

// MigrateData copies all application data from the source backend to the target
// backend, following the drop-and-recreate model: the target schema is ensured
// via migrations, its application rows are wiped, and fresh rows are copied from
// the source. This is the single code path for both directions — the dialect
// layer handles write-side differences — and it is idempotent: a failed or
// interrupted run leaves the marker untouched, so re-running simply wipes any
// partial copy and redoes it.
//
// The caller is responsible for ensuring the source is quiescent (no concurrent
// writers) for the duration of the copy so the snapshot is consistent.
func MigrateData(ctx context.Context, src, dst Config, progress ProgressFunc) (*MigrateReport, error) {
	srcDB, err := NewDB(src)
	if err != nil {
		return nil, fmt.Errorf("open source database: %w", err)
	}
	defer srcDB.Close()

	dstDB, err := NewDB(dst)
	if err != nil {
		return nil, fmt.Errorf("open target database: %w", err)
	}
	defer dstDB.Close()

	srcConn := srcDB.Connection()
	dstConn := dstDB.Connection()
	srcDialect := srcDB.Dialect()
	dstDialect := dstDB.Dialect()

	progress.emit("Migrating %s -> %s", srcDialect, dstDialect)

	report := &MigrateReport{
		SourceBackend: string(srcDialect),
		TargetBackend: string(dstDialect),
		RowsPerTable:  map[string]int64{},
	}

	// Clear + copy run in a single target transaction for atomicity: any
	// failure rolls back to the target's prior state, and the marker is never
	// advanced, so the migration can be retried cleanly.
	tx, err := dstConn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin target transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Wipe existing application rows, children before parents.
	for i := len(appCopyOrder) - 1; i >= 0; i-- {
		table := appCopyOrder[i]
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return nil, fmt.Errorf("clear target table %s: %w", table, err)
		}
	}

	// Copy parents before children.
	for _, table := range appCopyOrder {
		n, err := copyTable(ctx, srcConn, dstConn, tx, srcDialect, dstDialect, table, progress)
		if err != nil {
			return nil, fmt.Errorf("copy table %s: %w", table, err)
		}
		report.RowsPerTable[table] = n
		report.TotalRows += n
		progress.emit("  %-32s %d rows", table, n)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit target transaction: %w", err)
	}

	// Explicit IDs were inserted into PostgreSQL identity columns without
	// advancing their sequences; reset each so the next insert does not collide.
	if dstDialect == DialectPostgres {
		if err := resetPostgresSequences(ctx, dstConn, progress); err != nil {
			return nil, fmt.Errorf("reset sequences: %w", err)
		}
	}

	// Verify row counts match before declaring success.
	if err := verifyCounts(ctx, srcConn, dstConn, report, progress); err != nil {
		return nil, err
	}

	progress.emit("Migration complete: %d rows across %d tables", report.TotalRows, len(appCopyOrder))
	return report, nil
}

// copyTable streams rows of a single table from source to target, coercing
// values for the target dialect (notably integer<->boolean, since SQLite has no
// native boolean type). Only columns present in both schemas are copied.
// Introspection reads the schema (identical inside or outside the tx) via the
// plain connections; inserts go through the migration transaction.
func copyTable(ctx context.Context, src, dst *sql.DB, tx *sql.Tx, srcD, dstD Dialect, table string, progress ProgressFunc) (int64, error) {
	_, srcCols, err := columnTypes(ctx, src, srcD, table)
	if err != nil {
		return 0, fmt.Errorf("introspect source columns: %w", err)
	}
	dstTypes, _, err := columnTypes(ctx, dst, dstD, table)
	if err != nil {
		return 0, fmt.Errorf("introspect target columns: %w", err)
	}

	// Intersect, preserving source order.
	var cols []string
	for _, c := range srcCols {
		if _, ok := dstTypes[c]; ok {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return 0, fmt.Errorf("no shared columns for table %s", table)
	}

	colList := strings.Join(cols, ", ")
	rows, err := src.QueryContext(ctx, "SELECT "+colList+" FROM "+table)
	if err != nil {
		return 0, fmt.Errorf("read source rows: %w", err)
	}
	defer rows.Close()

	insertSQL := "INSERT INTO " + table + " (" + colList + ") VALUES (" + placeholders(dstD, len(cols)) + ")"
	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare target insert: %w", err)
	}
	defer stmt.Close()

	var count int64
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return 0, fmt.Errorf("scan source row: %w", err)
		}
		for i, c := range cols {
			vals[i] = coerce(vals[i], dstTypes[c], dstD)
		}
		if _, err := stmt.ExecContext(ctx, vals...); err != nil {
			return 0, fmt.Errorf("insert target row: %w", err)
		}
		count++
		if count%10000 == 0 {
			progress.emit("    %s: %d rows...", table, count)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate source rows: %w", err)
	}
	return count, nil
}

// coerce adapts a scanned value to the target column's dialect/type. The main
// hazard is boolean storage: SQLite stores booleans as integer 0/1, PostgreSQL
// uses a native boolean type, so a value moving between them must be converted
// based on the destination column type.
func coerce(val any, dstType string, dstDialect Dialect) any {
	if val == nil {
		return nil
	}
	isBoolCol := strings.Contains(strings.ToLower(dstType), "bool")
	switch v := val.(type) {
	case bool:
		if dstDialect == DialectSQLite {
			if v {
				return int64(1)
			}
			return int64(0)
		}
		return v
	case int64:
		if dstDialect == DialectPostgres && isBoolCol {
			return v != 0
		}
		return v
	default:
		return val
	}
}

// placeholders returns "?, ?, ..." for SQLite or "$1, $2, ..." for PostgreSQL.
func placeholders(d Dialect, n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		if d == DialectPostgres {
			parts[i] = fmt.Sprintf("$%d", i+1)
		} else {
			parts[i] = "?"
		}
	}
	return strings.Join(parts, ", ")
}

// columnTypes introspects a table's columns, returning a name->declared-type map
// and the column names in schema order.
func columnTypes(ctx context.Context, db *sql.DB, d Dialect, table string) (map[string]string, []string, error) {
	types := map[string]string{}
	var order []string

	if d == DialectPostgres {
		rows, err := db.QueryContext(ctx,
			"SELECT column_name, data_type FROM information_schema.columns WHERE table_name = $1 ORDER BY ordinal_position", table)
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var name, typ string
			if err := rows.Scan(&name, &typ); err != nil {
				return nil, nil, err
			}
			types[name] = typ
			order = append(order, name)
		}
		return types, order, rows.Err()
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt *string
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, nil, err
		}
		types[name] = ctype
		order = append(order, name)
	}
	return types, order, rows.Err()
}

// resetPostgresSequences sets each identity sequence to MAX(id) so the next
// generated id does not collide with copied explicit ids. Tables without an
// "id" serial sequence are skipped.
func resetPostgresSequences(ctx context.Context, dst *sql.DB, progress ProgressFunc) error {
	for _, table := range appCopyOrder {
		var hasID bool
		if err := dst.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name='id')",
			table).Scan(&hasID); err != nil {
			return fmt.Errorf("check id column for %s: %w", table, err)
		}
		if !hasID {
			continue
		}
		var seq sql.NullString
		if err := dst.QueryRowContext(ctx, "SELECT pg_get_serial_sequence($1, 'id')", table).Scan(&seq); err != nil {
			return fmt.Errorf("resolve sequence for %s: %w", table, err)
		}
		if !seq.Valid || seq.String == "" {
			continue
		}
		// is_called = (row count > 0): when populated, nextval yields MAX+1;
		// when empty, nextval yields 1.
		q := fmt.Sprintf(
			"SELECT setval('%s', COALESCE((SELECT MAX(id) FROM %s), 1), (SELECT COUNT(*) FROM %s) > 0)",
			seq.String, table, table)
		if _, err := dst.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("setval for %s: %w", table, err)
		}
		progress.emit("  reset sequence %s", seq.String)
	}
	return nil
}

// verifyCounts confirms per-table row counts match between source and target.
func verifyCounts(ctx context.Context, src, dst *sql.DB, report *MigrateReport, progress ProgressFunc) error {
	for _, table := range appCopyOrder {
		var srcN, dstN int64
		if err := src.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&srcN); err != nil {
			return fmt.Errorf("count source %s: %w", table, err)
		}
		if err := dst.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&dstN); err != nil {
			return fmt.Errorf("count target %s: %w", table, err)
		}
		if srcN != dstN {
			return fmt.Errorf("verification failed for %s: source %d rows, target %d rows", table, srcN, dstN)
		}
	}
	progress.emit("Verified row counts for %d tables", len(appCopyOrder))
	return nil
}

// BackupToSQL writes an INSERT-statement dump of all application tables from cfg
// to a timestamped .sql file inside backupDir, creating the directory if needed.
// Call before wiping the target database so data is recoverable. Returns the
// path of the file written.
func BackupToSQL(ctx context.Context, cfg Config, backupDir string, progress ProgressFunc) (string, error) {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	db, err := NewDB(cfg)
	if err != nil {
		return "", fmt.Errorf("open database for backup: %w", err)
	}
	defer db.Close()

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("altmount-%s-migration-%s.sql", cfg.Type, ts)
	filePath := filepath.Join(backupDir, filename)

	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("create backup file: %w", err)
	}
	defer f.Close()

	conn := db.Connection()
	dialect := db.Dialect()

	fmt.Fprintf(f, "-- AltMount %s backup before migration — %s\n\n", cfg.Type, ts)

	for _, table := range appCopyOrder {
		var count int64
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			continue
		}
		if count == 0 {
			continue
		}

		_, cols, err := columnTypes(ctx, conn, dialect, table)
		if err != nil {
			progress.emit("  skipping backup of %s: %v", table, err)
			continue
		}

		colList := strings.Join(cols, ", ")
		rows, err := conn.QueryContext(ctx, "SELECT "+colList+" FROM "+table)
		if err != nil {
			return "", fmt.Errorf("read %s for backup: %w", table, err)
		}

		fmt.Fprintf(f, "-- %s (%d rows)\n", table, count)
		var written int64
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return "", fmt.Errorf("scan row from %s: %w", table, err)
			}
			fmt.Fprintf(f, "INSERT INTO %s (%s) VALUES (", table, colList)
			for i, v := range vals {
				if i > 0 {
					fmt.Fprint(f, ", ")
				}
				fmt.Fprint(f, sqlLiteral(v))
			}
			fmt.Fprintln(f, ");")
			written++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("iterate %s for backup: %w", table, err)
		}
		fmt.Fprintln(f)
		progress.emit("  backed up %s: %d rows", table, written)
	}

	return filePath, nil
}

// sqlLiteral formats a scanned database value as a SQL literal suitable for
// embedding in an INSERT statement.
func sqlLiteral(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case bool:
		if val {
			return "TRUE"
		}
		return "FALSE"
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%g", val)
	case string:
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	case []byte:
		return "'" + strings.ReplaceAll(string(val), "'", "''") + "'"
	case time.Time:
		return "'" + val.UTC().Format(time.RFC3339Nano) + "'"
	default:
		return fmt.Sprintf("'%v'", v)
	}
}

// RowCounts opens the given backend, sums application-table row counts, and
// closes. Used for pre-flight confirmation summaries (e.g. "target is not
// empty and will be wiped"). Opening runs migrations, so the schema is ensured
// as a side effect — harmless and idempotent.
func RowCounts(ctx context.Context, cfg Config) (map[string]int64, int64, error) {
	db, err := NewDB(cfg)
	if err != nil {
		return nil, 0, err
	}
	defer db.Close()
	conn := db.Connection()

	counts := map[string]int64{}
	var total int64
	for _, table := range appCopyOrder {
		var n int64
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			return nil, 0, fmt.Errorf("count %s: %w", table, err)
		}
		counts[table] = n
		total += n
	}
	return counts, total, nil
}
