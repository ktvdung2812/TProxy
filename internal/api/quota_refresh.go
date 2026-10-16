package api

import (
	"context"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/providers"
	"github.com/tproxy/tproxy/internal/store"
)

func (s *Server) refreshAccountQuotas(ctx context.Context) {
	items, err := s.store.Providers(ctx)
	if err != nil {
		return
	}
	jobs := make(chan store.ProviderCredential)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				_, _ = s.router.RefreshQuotaIfDue(ctx, job.Provider, job.Credential)
			}
		}()
	}
	for _, provider := range items {
		if !provider.Enabled || (!providers.SupportsQuota(provider.Type) && !providers.SupportsQuota(provider.ID)) {
			continue
		}
		credentials, err := s.store.Credentials(ctx, provider.ID)
		if err != nil {
			continue
		}
		for _, credential := range credentials {
			if (!credential.Enabled && !store.QuotaAutoDisabled(credential.Metadata)) || store.AccountExpired(credential, time.Now()) || credential.Status == "auth_required" {
				continue
			}
			select {
			case jobs <- store.ProviderCredential{Provider: provider, Credential: credential}:
			case <-ctx.Done():
				close(jobs)
				workers.Wait()
				return
			}
		}
	}
	close(jobs)
	workers.Wait()
}

func (s *Server) runQuotaRefresh(ctx context.Context) {
	s.refreshAccountQuotas(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshAccountQuotas(ctx)
		}
	}
}
