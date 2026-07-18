package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/tproxy/tproxy/internal/api"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/pricing"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	configPath := flag.String("config", "config.yaml", "path to tproxy YAML config")
	printKey := flag.Bool("print-master-key", false, "print a new base64 master key and exit")
	backupPath := flag.String("backup-database", "", "write a consistent SQLite backup and exit")
	restorePath := flag.String("restore-database", "", "restore a SQLite backup into the configured database and exit")
	integrityCheck := flag.Bool("integrity-check", false, "run SQLite integrity checks and exit")
	exportAuthPath := flag.String("export-auth", "", "export encrypted OAuth credential envelopes and exit")
	importAuthPath := flag.String("import-auth", "", "import an encrypted OAuth credential bundle and exit")
	flag.Parse()
	if *printKey {
		key, err := security.GenerateMasterKey()
		if err != nil {
			return err
		}
		fmt.Println(key)
		return nil
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *restorePath != "" {
		if err = store.RestoreSQLite(context.Background(), *restorePath, cfg.Database.DSN); err != nil {
			return err
		}
		log.Printf("restored SQLite database from %s", *restorePath)
		return nil
	}
	masterKey := config.Env(cfg.Security.MasterKeyEnv)
	encryptor, err := security.NewEncryptor(masterKey)
	if err != nil {
		return err
	}
	dataStore, err := store.OpenSQLite(cfg.Database.DSN, encryptor)
	if err != nil {
		return err
	}
	defer dataStore.Close()
	if *backupPath != "" {
		if err = dataStore.Backup(context.Background(), *backupPath); err != nil {
			return err
		}
		log.Printf("created SQLite backup at %s", *backupPath)
		return nil
	}
	if *integrityCheck {
		if err = dataStore.IntegrityCheck(context.Background()); err != nil {
			return err
		}
		log.Println("SQLite integrity check passed")
		return nil
	}
	if *exportAuthPath != "" {
		if err = dataStore.ExportAuthFile(context.Background(), *exportAuthPath); err != nil {
			return err
		}
		log.Printf("exported encrypted OAuth credentials to %s", *exportAuthPath)
		return nil
	}
	if *importAuthPath != "" {
		// Auth bundles reference provider IDs but intentionally contain no
		// provider topology. Seed the configured topology before importing into a
		// fresh database so provider validation remains strict and deterministic.
		if err = dataStore.Seed(context.Background(), cfg); err != nil {
			return err
		}
		if err = dataStore.ImportAuthFile(context.Background(), *importAuthPath); err != nil {
			return err
		}
		log.Printf("imported encrypted OAuth credentials from %s", *importAuthPath)
		return nil
	}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		return err
	}
	if err = dataStore.RecordConfigVersion(context.Background(), "startup", cfg); err != nil {
		log.Printf("warning: record startup config version: %v", err)
	}
	runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	requestRouter := router.New(dataStore, providers.NewRegistry())
	pricingCatalog := pricing.NewCatalog(pricing.Options{CachePath: pricing.DefaultCachePath(cfg.Database.DSN)})
	pricingCatalog.Start(runCtx, time.Hour)
	requestRouter.SetPricingCatalog(pricingCatalog)
	requestRouter.SetAllowUpstreamModels(cfg.Server.AllowUpstreamModels)
	requestRouter.ConfigureRouting(cfg.Routing)
	server := api.NewServer(cfg, dataStore, requestRouter)
	server.StartBackground(runCtx)
	defer server.Close()
	server.SetConfigPath(*configPath)
	httpServer := &http.Server{Addr: net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)), Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 120 * time.Second}
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", httpServer.Addr, err)
	}
	defer listener.Close()
	log.Printf("tproxy listening on http://%s", httpServer.Addr)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- httpServer.Serve(listener)
	}()
	select {
	case err = <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("tproxy server stopped: %w", err)
		}
	case <-runCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			// Shutdown may leave active streams behind after its deadline. Close
			// the listener so the serving goroutine cannot keep the process alive.
			_ = httpServer.Close()
		}
		if err = <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			if shutdownErr != nil {
				return fmt.Errorf("graceful shutdown failed: %v; server stopped: %w", shutdownErr, err)
			}
			return fmt.Errorf("tproxy server stopped during shutdown: %w", err)
		}
		if shutdownErr != nil {
			return fmt.Errorf("graceful shutdown failed: %w", shutdownErr)
		}
	}
	return nil
}
