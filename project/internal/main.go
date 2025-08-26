package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	_ "time"

	"github.com/aarushishahhh/linkwatch/project/internal/api"
	"github.com/aarushishahhh/linkwatch/project/internal/checker"
	"github.com/aarushishahhh/linkwatch/project/internal/config"
	"github.com/aarushishahhh/linkwatch/project/internal/storage"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Initialize database
	db, err := initDB(cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	store := storage.New(db)
	if err := store.Migrate(); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Initialize checker
	chk := checker.New(store, checker.Config{
		Interval:       cfg.CheckInterval,
		MaxConcurrency: cfg.MaxConcurrency,
		HTTPTimeout:    cfg.HTTPTimeout,
	})

	// Initialize API server
	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: api.NewRouter(store),
	}

	// Start background checker
	ctx, cancel := context.WithCancel(context.Background())
	chk.Start(ctx)

	// Start HTTP server
	go func() {
		slog.Info("starting server", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("shutting down gracefully")
	cancel() // Stop checker

	// Shutdown HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown failed", "error", err)
	}

	slog.Info("shutdown complete")
}

func initDB(databaseURL string) (*sql.DB, error) {
	if databaseURL == "" {
		// Supporting SQLite
		databaseURL = "sqlite3://linkwatch.db"
	}

	var driver, dsn string
	if databaseURL[:9] == "sqlite3://" {
		driver = "sqlite3"
		dsn = databaseURL[9:]
	} else if databaseURL[:11] == "postgres://" || databaseURL[:13] == "postgresql://" {
		driver = "postgres"
		dsn = databaseURL
	} else {
		driver = "sqlite3"
		dsn = databaseURL
	}

	return sql.Open(driver, dsn)
}
