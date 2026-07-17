package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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
			log.Fatal(err)
		}
		fmt.Println(key)
		return
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *restorePath != "" {
		if err = store.RestoreSQLite(context.Background(), *restorePath, cfg.Database.DSN); err != nil {
			log.Fatal(err)
		}
		log.Printf("restored SQLite database from %s", *restorePath)
		return
	}
	masterKey := config.Env(cfg.Security.MasterKeyEnv)
	encryptor, err := security.NewEncryptor(masterKey)
	if err != nil {
		log.Fatal(err)
	}
	dataStore, err := store.OpenSQLite(cfg.Database.DSN, encryptor)
	if err != nil {
		log.Fatal(err)
	}
	defer dataStore.Close()
	if *backupPath != "" {
		if err = dataStore.Backup(context.Background(), *backupPath); err != nil {
			log.Fatal(err)
		}
		log.Printf("created SQLite backup at %s", *backupPath)
		return
	}
	if *integrityCheck {
		if err = dataStore.IntegrityCheck(context.Background()); err != nil {
			log.Fatal(err)
		}
		log.Println("SQLite integrity check passed")
		return
	}
	if *exportAuthPath != "" {
		if err = dataStore.ExportAuthFile(context.Background(), *exportAuthPath); err != nil {
			log.Fatal(err)
		}
		log.Printf("exported encrypted OAuth credentials to %s", *exportAuthPath)
		return
	}
	if *importAuthPath != "" {
		if err = dataStore.ImportAuthFile(context.Background(), *importAuthPath); err != nil {
			log.Fatal(err)
		}
		log.Printf("imported encrypted OAuth credentials from %s", *importAuthPath)
		return
	}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
	if err = dataStore.RecordConfigVersion(context.Background(), "startup", cfg); err != nil {
		log.Printf("warning: record startup config version: %v", err)
	}
	requestRouter := router.New(dataStore, providers.NewRegistry())
	pricingCatalog := pricing.NewCatalog(pricing.Options{CachePath: pricing.DefaultCachePath(cfg.Database.DSN)})
	pricingCatalog.Start(context.Background(), time.Hour)
	requestRouter.SetPricingCatalog(pricingCatalog)
	requestRouter.SetAllowUpstreamModels(cfg.Server.AllowUpstreamModels)
	requestRouter.ConfigureRouting(cfg.Routing)
	server := api.NewServer(cfg, dataStore, requestRouter)
	server.StartBackground(context.Background())
	defer server.Close()
	server.SetConfigPath(*configPath)
	httpServer := &http.Server{Addr: fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port), Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 120 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()
	log.Printf("tproxy listening on http://%s", httpServer.Addr)
	if err = httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
