package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/javi11/altmount/frontend"
	"github.com/javi11/altmount/internal/api"
	"github.com/javi11/altmount/internal/config"
	"github.com/javi11/altmount/internal/database"
)

// maintenanceState tracks the status of the in-flight or completed migration.
type maintenanceState struct {
	mu       sync.Mutex
	status   string   // "ready" | "running" | "done" | "error"
	progress []string // accumulated log lines (capped at 500)
	errMsg   string
	report   *database.MigrateReport
}

// runMaintenanceBoot starts a minimal HTTP server that drives a backend data
// migration. It blocks until the process receives SIGINT/SIGTERM or the server
// hits an internal error. Background workers are never started; only the
// migration API and the SPA are served.
//
// After a successful migration the marker is written to markerPath and the
// status endpoint reflects "done". The operator then restarts the container to
// complete the switch.
func runMaintenanceBoot(cfg *config.Config, decision database.BootDecision, markerPath string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling must be set up here — we branched out of runServe before
	// it reached its own signal setup.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(sigChan)
	go func() {
		select {
		case sig := <-sigChan:
			slog.InfoContext(ctx, "Maintenance server: received shutdown signal", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	slog.InfoContext(ctx, "Entering maintenance mode",
		"source", decision.SourceType,
		"target", decision.TargetType,
		"reason", decision.Reason)

	srcCfg := maintenanceDBConfig(decision.SourceType, cfg)
	dstCfg := maintenanceDBConfig(decision.TargetType, cfg)

	// Pre-fetch source row counts once at startup — they won't change while
	// maintenance is active and workers are stopped.
	srcCounts, srcTotal, srcCountsErr := database.RowCounts(ctx, srcCfg)
	if srcCountsErr != nil {
		slog.WarnContext(ctx, "Could not read source row counts (source may be unreachable)",
			"err", srcCountsErr)
	}

	state := &maintenanceState{status: "ready"}

	app := fiber.New(fiber.Config{
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // no timeout — migrations can run for many minutes
		IdleTimeout:  60 * time.Second,
	})
	app.Use(recover.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept",
		AllowMethods: "GET, POST, OPTIONS",
	}))

	// Maintenance API — no auth: there is no app database to authenticate against
	// during this mode.
	mg := app.Group("/api")

	// GET /api/maintenance/status
	// Returns the migration parameters and current state. Polled by the frontend.
	mg.Get("/maintenance/status", func(c *fiber.Ctx) error {
		state.mu.Lock()
		status := state.status
		progress := append([]string(nil), state.progress...)
		errMsg := state.errMsg
		state.mu.Unlock()

		resp := fiber.Map{
			"mode":          "maintenance",
			"source":        decision.SourceType,
			"target":        decision.TargetType,
			"reason":        decision.Reason,
			"source_total":  srcTotal,
			"source_counts": srcCounts,
			"status":        status,
			"progress":      progress,
			"error":         errMsg,
		}
		if srcCountsErr != nil {
			resp["source_error"] = srcCountsErr.Error()
		}
		return c.JSON(resp)
	})

	// POST /api/maintenance/run
	// Starts the migration in the background. Returns 409 if already running or done.
	mg.Post("/maintenance/run", func(c *fiber.Ctx) error {
		state.mu.Lock()
		switch state.status {
		case "running":
			state.mu.Unlock()
			return api.RespondConflict(c, "migration already in progress", "poll /api/maintenance/status for updates")
		case "done":
			state.mu.Unlock()
			return api.RespondConflict(c, "migration already completed", "restart the server to apply the new backend")
		}
		state.status = "running"
		state.progress = nil
		state.errMsg = ""
		state.mu.Unlock()

		go func() {
			prog := database.ProgressFunc(func(line string) {
				state.mu.Lock()
				if len(state.progress) < 500 {
					state.progress = append(state.progress, line)
				}
				state.mu.Unlock()
				slog.InfoContext(ctx, "migration: "+line)
			})

			report, err := database.MigrateData(ctx, srcCfg, dstCfg, prog)

			state.mu.Lock()
			defer state.mu.Unlock()

			if err != nil {
				state.status = "error"
				state.errMsg = err.Error()
				slog.ErrorContext(ctx, "Maintenance migration failed", "err", err)
				return
			}

			// Flip the marker — this is the commit point. The marker is only
			// written after a fully verified successful copy.
			var schemaVer int64
			if dstDB, openErr := database.NewDB(dstCfg); openErr == nil {
				schemaVer, _ = database.SchemaVersion(dstDB.Connection(), dstDB.Dialect())
				dstDB.Close()
			}
			if writeErr := database.WriteState(markerPath, database.DBState{
				Backend:       decision.TargetType,
				SchemaVersion: schemaVer,
			}); writeErr != nil {
				slog.ErrorContext(ctx, "Migration data written but marker update failed", "err", writeErr)
				state.progress = append(state.progress,
					"WARNING: data migrated but marker write failed: "+writeErr.Error())
			}

			state.report = report
			state.status = "done"
			slog.InfoContext(ctx, "Maintenance migration completed successfully",
				"total_rows", report.TotalRows,
				"tables", len(report.RowsPerTable))
		}()

		return api.RespondMessage(c, "migration started")
	})

	// POST /api/config/database/test-connection
	// Validates a DSN or path is reachable without running migrations.
	mg.Post("/config/database/test-connection", maintenanceHandleTestConnection)

	// Liveness probe — returns "maintenance" so health checks know we are not
	// in normal operation.
	app.Get("/live", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":    "maintenance",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	// SPA fallback — serve the same embedded frontend so the browser can render
	// the maintenance takeover screen.
	setupMaintenanceSPARoutes(app)

	addr := fmt.Sprintf(":%d", cfg.WebDAV.Port)
	slog.InfoContext(ctx, "Maintenance mode server listening", "port", cfg.WebDAV.Port)

	serverErr := make(chan error, 1)
	go func() {
		if err := app.Listen(addr); err != nil {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.InfoContext(ctx, "Maintenance server shutting down")
	case err := <-serverErr:
		return fmt.Errorf("maintenance server error: %w", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return app.ShutdownWithContext(shutdownCtx)
}

// maintenanceDBConfig builds a database.Config for one side of the migration.
func maintenanceDBConfig(backendType string, cfg *config.Config) database.Config {
	if backendType == "postgres" {
		return database.Config{Type: "postgres", DSN: cfg.Database.DSN}
	}
	return database.Config{Type: "sqlite", DatabasePath: cfg.Database.Path}
}

// maintenanceHandleTestConnection validates a database connection string or path
// is reachable without running migrations.
func maintenanceHandleTestConnection(c *fiber.Ctx) error {
	var req struct {
		Type string `json:"type"`
		Path string `json:"path"`
		DSN  string `json:"dsn"`
	}
	if err := c.BodyParser(&req); err != nil {
		return api.RespondBadRequest(c, "invalid request body", err.Error())
	}
	if req.Type != "sqlite" && req.Type != "postgres" {
		return api.RespondBadRequest(c, "type must be sqlite or postgres", "")
	}
	cfg := database.Config{Type: req.Type, DatabasePath: req.Path, DSN: req.DSN}
	if err := database.PingDB(c.Context(), cfg); err != nil {
		return api.RespondBadRequest(c, "connection test failed", err.Error())
	}
	return api.RespondMessage(c, "connection successful")
}

// setupMaintenanceSPARoutes wires the embedded (or on-disk) frontend SPA.
func setupMaintenanceSPARoutes(app *fiber.App) {
	buildFS, err := frontend.GetBuildFS()
	if err != nil {
		// Docker / development: serve static files from disk.
		app.All("/*", filesystem.New(filesystem.Config{
			Root:         http.Dir(frontendBuildPath),
			NotFoundFile: "index.html",
			Index:        "index.html",
		}))
		return
	}
	app.All("/*", filesystem.New(filesystem.Config{
		Root:         http.FS(buildFS),
		NotFoundFile: "index.html",
		Index:        "index.html",
	}))
}
