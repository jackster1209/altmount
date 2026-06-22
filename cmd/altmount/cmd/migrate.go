package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/javi11/altmount/internal/config"
	"github.com/javi11/altmount/internal/database"
	"github.com/spf13/cobra"
)

func init() {
	migrateCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate Altmount data between SQLite and PostgreSQL",
		Long: `Copy all application data from one database backend to the other.

By default the TARGET is the backend configured in config.yaml and the SOURCE is
the other backend, so after switching "type" in config you can run:

    altmount migrate

The target is rebuilt from the source (drop-and-recreate): its existing
application rows are wiped and replaced. The source is never modified. The copy
must run while Altmount is NOT serving, so the source has no concurrent writers.`,
		RunE: runMigrate,
	}

	migrateCmd.Flags().String("source-type", "", "source backend: sqlite or postgres (default: opposite of target)")
	migrateCmd.Flags().String("source-path", "", "source SQLite path (default: config database path)")
	migrateCmd.Flags().String("source-dsn", "", "source PostgreSQL DSN (default: config DSN)")
	migrateCmd.Flags().String("target-type", "", "target backend (default: config database type)")
	migrateCmd.Flags().String("target-path", "", "target SQLite path (default: config database path)")
	migrateCmd.Flags().String("target-dsn", "", "target PostgreSQL DSN (default: config DSN)")
	migrateCmd.Flags().Bool("yes", false, "skip the confirmation prompt")

	rootCmd.AddCommand(migrateCmd)
}

func runMigrate(cmd *cobra.Command, _ []string) error {
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	flag := func(name string) string { v, _ := cmd.Flags().GetString(name); return v }
	skipConfirm, _ := cmd.Flags().GetBool("yes")

	// Target defaults to the configured backend.
	targetType := flag("target-type")
	if targetType == "" {
		targetType = cfg.Database.Type
	}
	if targetType == "" {
		targetType = "sqlite"
	}

	// Source defaults to the opposite of the target.
	sourceType := flag("source-type")
	if sourceType == "" {
		sourceType = opposite(targetType)
	}

	target, err := buildDBConfig(targetType, flag("target-path"), flag("target-dsn"), cfg, "target")
	if err != nil {
		return err
	}
	source, err := buildDBConfig(sourceType, flag("source-path"), flag("source-dsn"), cfg, "source")
	if err != nil {
		return err
	}

	if source.Type == target.Type {
		return fmt.Errorf("source and target are both %q; nothing to migrate", source.Type)
	}

	ctx := context.Background()

	// Pre-flight summary: source row counts and whether the target will be wiped.
	fmt.Printf("Source: %s\n", describe(source))
	fmt.Printf("Target: %s\n", describe(target))

	_, srcTotal, err := database.RowCounts(ctx, source)
	if err != nil {
		return fmt.Errorf("reading source: %w (is the source backend reachable?)", err)
	}
	_, dstTotal, err := database.RowCounts(ctx, target)
	if err != nil {
		return fmt.Errorf("reading target: %w (is the target backend reachable?)", err)
	}

	fmt.Printf("\nSource holds %d application rows.\n", srcTotal)
	if dstTotal > 0 {
		fmt.Printf("WARNING: target is NOT empty (%d rows). It will be WIPED and rebuilt from the source.\n", dstTotal)
	} else {
		fmt.Printf("Target is empty and will be populated from the source.\n")
	}

	if !skipConfirm {
		fmt.Print("\nProceed? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	fmt.Println()
	progress := database.ProgressFunc(func(s string) { fmt.Println(s) })
	if _, err := database.MigrateData(ctx, source, target, progress); err != nil {
		return fmt.Errorf("migration failed (target left unchanged, safe to retry): %w", err)
	}

	// Commit point: record the now-active backend so a subsequent server boot
	// sees matching state. Read the freshly-migrated target's schema version.
	if err := writeMarker(ctx, target); err != nil {
		// The data migrated successfully; a marker write failure is non-fatal
		// but worth surfacing.
		fmt.Printf("\nData migrated, but updating the state marker failed: %v\n", err)
		return nil
	}

	fmt.Printf("\nDone. Set database.type to %q in config.yaml (if not already) and restart Altmount.\n", target.Type)
	return nil
}

// buildDBConfig assembles a database.Config for one side, filling unset values
// from config.yaml and validating that the required coordinates are present.
func buildDBConfig(backend, path, dsn string, cfg *config.Config, role string) (database.Config, error) {
	switch backend {
	case "sqlite":
		if path == "" {
			path = cfg.Database.Path
		}
		if path == "" {
			return database.Config{}, fmt.Errorf("%s is sqlite but no path given (set --%s-path or database.path)", role, role)
		}
		return database.Config{Type: "sqlite", DatabasePath: path}, nil
	case "postgres":
		if dsn == "" {
			dsn = cfg.Database.DSN
		}
		if dsn == "" {
			return database.Config{}, fmt.Errorf("%s is postgres but no DSN given (set --%s-dsn or database.dsn)", role, role)
		}
		return database.Config{Type: "postgres", DSN: dsn}, nil
	default:
		return database.Config{}, fmt.Errorf("%s has unknown backend %q (want sqlite or postgres)", role, backend)
	}
}

func writeMarker(ctx context.Context, target database.Config) error {
	db, err := database.NewDB(target)
	if err != nil {
		return err
	}
	defer db.Close()
	version, err := database.SchemaVersion(db.Connection(), db.Dialect())
	if err != nil {
		return err
	}
	return database.WriteState(database.StatePath(configFile), database.DBState{
		Backend:       target.Type,
		SchemaVersion: version,
	})
}

func opposite(backend string) string {
	if backend == "postgres" {
		return "sqlite"
	}
	return "postgres"
}

func describe(c database.Config) string {
	if c.Type == "postgres" {
		return "postgres (" + redactDSN(c.DSN) + ")"
	}
	return "sqlite (" + c.DatabasePath + ")"
}

// redactDSN hides credentials in a postgres DSN for display.
func redactDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return dsn
	}
	scheme := strings.Index(dsn, "://")
	if scheme < 0 {
		return "***" + dsn[at:]
	}
	return dsn[:scheme+3] + "***" + dsn[at:]
}
