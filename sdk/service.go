package sdk

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tproxy/tproxy/internal/api"
	"github.com/tproxy/tproxy/internal/config"
	"github.com/tproxy/tproxy/internal/pricing"
	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/router"
	"github.com/tproxy/tproxy/internal/security"
	"github.com/tproxy/tproxy/internal/store"
)

type Service struct {
	config     *config.Config
	configPath string
	store      *store.Store
	handler    http.Handler
	apiServer  *api.Server
	server     *http.Server
}

type Builder struct {
	config     *config.Config
	configPath string
}

func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) WithConfig(cfg *config.Config) *Builder { b.config = cfg; return b }

func (b *Builder) WithConfigPath(path string) *Builder { b.configPath = path; return b }

func (b *Builder) Build() (*Service, error) {
	cfg := b.config
	if cfg == nil {
		if b.configPath == "" {
			return nil, fmt.Errorf("config or config path is required")
		}
		loaded, err := config.Load(b.configPath)
		if err != nil {
			return nil, err
		}
		cfg = loaded
	}
	masterKey := config.Env(cfg.Security.MasterKeyEnv)
	encryptor, err := security.NewEncryptor(masterKey)
	if err != nil {
		return nil, err
	}
	dataStore, err := store.OpenSQLite(cfg.Database.DSN, encryptor)
	if err != nil {
		return nil, err
	}
	if err = dataStore.Seed(context.Background(), cfg); err != nil {
		_ = dataStore.Close()
		return nil, err
	}
	requestRouter := router.New(dataStore, providers.NewRegistry())
	pricingCatalog := pricing.NewCatalog(pricing.Options{CachePath: pricing.DefaultCachePath(cfg.Database.DSN)})
	pricingCatalog.Start(context.Background(), time.Hour)
	requestRouter.SetPricingCatalog(pricingCatalog)
	requestRouter.SetAllowUpstreamModels(cfg.Server.AllowUpstreamModels)
	requestRouter.ConfigureRouting(cfg.Routing)
	server := api.NewServer(cfg, dataStore, requestRouter)
	server.SetConfigPath(b.configPath)
	return &Service{config: cfg, configPath: b.configPath, store: dataStore, handler: server.Handler(), apiServer: server}, nil
}

func (s *Service) Handler() http.Handler { return s.handler }

func (s *Service) Backup(ctx context.Context, destination string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service is not initialized")
	}
	return s.store.Backup(ctx, destination)
}

func (s *Service) IntegrityCheck(ctx context.Context) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("service is not initialized")
	}
	return s.store.IntegrityCheck(ctx)
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil || s.config == nil {
		return fmt.Errorf("service is not initialized")
	}
	s.apiServer.StartBackground(ctx)
	defer s.apiServer.Close()
	s.server = &http.Server{Addr: fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port), Handler: s.handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- s.server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		_ = s.server.Shutdown(context.Background())
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.server != nil {
		_ = s.server.Shutdown(ctx)
	}
	if s.apiServer != nil {
		s.apiServer.Close()
	}
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}
