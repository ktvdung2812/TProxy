package auth

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/tproxy/tproxy/internal/store"
)

type PrewarmConfig struct {
	CheckInterval        time.Duration
	PrewarmBeforeExpiry  time.Duration
	MaxConcurrentPrewarm int
	TopNAccounts         int
	IdleAccountTTL       time.Duration
}

type accountActivity struct {
	accountID      string
	providerID     string
	providerType   string
	lastUsedAt     time.Time
	requestCount   int
	tokenExpiresAt time.Time
}

type PrewarmManager struct {
	cfg      PrewarmConfig
	mu       sync.Mutex
	accounts map[string]accountActivity
	stats    struct {
		completed int
		failed    int
		lastRun   time.Time
	}
	stopCh chan struct{}
	doneCh chan struct{}
}

func NewPrewarmManager() *PrewarmManager {
	return &PrewarmManager{
		cfg: PrewarmConfig{
			CheckInterval:        30 * time.Second,
			PrewarmBeforeExpiry:  5 * time.Minute,
			MaxConcurrentPrewarm: 3,
			TopNAccounts:         10,
			IdleAccountTTL:       10 * time.Minute,
		},
		accounts: make(map[string]accountActivity),
	}
}

func (p *PrewarmManager) RecordActivity(accountID, providerID, providerType string, tokenExpiresAt time.Time) {
	if accountID == "" || providerID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	existing := p.accounts[accountID]
	existing.accountID = accountID
	existing.providerID = providerID
	existing.providerType = providerType
	existing.lastUsedAt = time.Now()
	existing.requestCount++
	if !tokenExpiresAt.IsZero() {
		existing.tokenExpiresAt = tokenExpiresAt
	}
	p.accounts[accountID] = existing
}

func (p *PrewarmManager) Start(ctx context.Context, refresh func(context.Context, string, string) error) {
	if p == nil || refresh == nil {
		return
	}
	p.mu.Lock()
	if p.stopCh != nil {
		p.mu.Unlock()
		return
	}
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	p.mu.Unlock()
	go func() {
		defer close(p.doneCh)
		ticker := time.NewTicker(p.cfg.CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.runCycle(ctx, refresh)
			case <-p.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (p *PrewarmManager) Stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	stopCh := p.stopCh
	doneCh := p.doneCh
	p.stopCh = nil
	p.doneCh = nil
	p.mu.Unlock()
	if stopCh != nil {
		close(stopCh)
		<-doneCh
	}
}

func (p *PrewarmManager) runCycle(ctx context.Context, refresh func(context.Context, string, string) error) {
	now := time.Now()
	candidates := p.topCandidates(now)
	if len(candidates) == 0 {
		return
	}
	sem := make(chan struct{}, p.cfg.MaxConcurrentPrewarm)
	var wg sync.WaitGroup
	for _, item := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(accountID, providerID string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := refresh(ctx, accountID, providerID); err != nil {
				p.mu.Lock()
				p.stats.failed++
				p.mu.Unlock()
				return
			}
			p.mu.Lock()
			p.stats.completed++
			p.mu.Unlock()
		}(item.accountID, item.providerID)
	}
	wg.Wait()
	p.mu.Lock()
	p.stats.lastRun = now
	p.mu.Unlock()
}

func (p *PrewarmManager) topCandidates(now time.Time) []accountActivity {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, item := range p.accounts {
		if now.Sub(item.lastUsedAt) > p.cfg.IdleAccountTTL {
			delete(p.accounts, id)
		}
	}
	items := make([]accountActivity, 0, len(p.accounts))
	for _, item := range p.accounts {
		if item.tokenExpiresAt.IsZero() {
			continue
		}
		if item.tokenExpiresAt.Sub(now) <= p.cfg.PrewarmBeforeExpiry {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].requestCount == items[j].requestCount {
			return items[i].lastUsedAt.After(items[j].lastUsedAt)
		}
		return items[i].requestCount > items[j].requestCount
	})
	if len(items) > p.cfg.TopNAccounts {
		items = items[:p.cfg.TopNAccounts]
	}
	return items
}

func (m *Manager) RecordActivity(provider store.Provider, credential store.Credential) {
	if m == nil || m.prewarm == nil {
		return
	}
	var expiresAt time.Time
	if credential.OAuthToken != nil {
		expiresAt = credential.OAuthToken.ExpiresAt
	}
	if raw := stringValue(credential.Metadata["copilot_token_expires_at"]); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			expiresAt = parsed
		}
	}
	if raw := stringValue(credential.Metadata["vertex_access_expires_at"]); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			expiresAt = parsed
		}
	}
	m.prewarm.RecordActivity(credential.ID, provider.ID, provider.Type, expiresAt)
}

func (m *Manager) prewarmRefresh(ctx context.Context, credentialID, providerID string) error {
	credential, err := m.store.CredentialByID(ctx, credentialID)
	if err != nil {
		return err
	}
	provider, err := m.store.Provider(ctx, providerID)
	if err != nil {
		return err
	}
	_, err = m.EnsureValid(ctx, *provider, credential, false)
	return err
}
