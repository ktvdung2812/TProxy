package store

import (
	"context"
	"fmt"
)

// MediaJobScope counts a shared resource, independently of the requesting key.
type MediaJobScope struct {
	Kind  string
	ID    string
	Limit int
}

type MediaJobOwner struct{ ProviderID, ClientAPIKeyID, Team string }

type MediaJobLimitError struct{ Scope MediaJobScope }

func (e *MediaJobLimitError) Error() string {
	return fmt.Sprintf("active media job limit exceeded for %s:%s", e.Scope.Kind, e.Scope.ID)
}

type MediaJobReservation struct{ store *Store }

func (scope MediaJobScope) matches(owner MediaJobOwner) bool {
	switch scope.Kind {
	case "global":
		return true
	case "key":
		return scope.ID == owner.ClientAPIKeyID
	case "team":
		return scope.ID == owner.Team
	case "provider":
		return scope.ID == owner.ProviderID
	default:
		return false
	}
}

func (s *Store) activeMediaJobs(ctx context.Context, scope MediaJobScope) (int, error) {
	query := `SELECT COUNT(*) FROM media_jobs WHERE lower(status) NOT IN ('completed','succeeded','failed','cancelled','canceled','expired')`
	var args []any
	switch scope.Kind {
	case "global":
	case "key":
		query += ` AND client_api_key_id=?`
		args = append(args, scope.ID)
	case "provider":
		query += ` AND provider_id=?`
		args = append(args, scope.ID)
	case "team":
		query += ` AND client_api_key_id IN (SELECT id FROM api_keys WHERE json_extract(policy_json,'$.team')=?)`
		args = append(args, scope.ID)
	default:
		return 0, fmt.Errorf("invalid media job scope %q", scope.Kind)
	}
	var count int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *Store) ActiveMediaJobsInScope(ctx context.Context, scope MediaJobScope) (int, error) {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	return s.mediaJobCountLocked(ctx, scope)
}

func (s *Store) mediaJobCountLocked(ctx context.Context, scope MediaJobScope) (int, error) {
	count, err := s.activeMediaJobs(ctx, scope)
	for _, owner := range s.mediaPending {
		if scope.matches(owner) {
			count++
		}
	}
	return count, err
}

// ReserveMediaJob checks all scopes atomically. No lock is held during upstream IO.
func (s *Store) ReserveMediaJob(ctx context.Context, owner MediaJobOwner, scopes []MediaJobScope) (*MediaJobReservation, error) {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	for _, scope := range scopes {
		if scope.Limit <= 0 {
			continue
		}
		count, err := s.mediaJobCountLocked(ctx, scope)
		if err != nil {
			return nil, err
		}
		if count >= scope.Limit {
			return nil, &MediaJobLimitError{Scope: scope}
		}
	}
	reservation := &MediaJobReservation{store: s}
	if s.mediaPending == nil {
		s.mediaPending = make(map[*MediaJobReservation]MediaJobOwner)
	}
	s.mediaPending[reservation] = owner
	return reservation, nil
}

func (r *MediaJobReservation) Release() {
	if r == nil {
		return
	}
	r.store.mediaMu.Lock()
	defer r.store.mediaMu.Unlock()
	delete(r.store.mediaPending, r)
}

// Save transfers the reservation to the persisted job without a counting gap.
func (r *MediaJobReservation) Save(ctx context.Context, job MediaJob) error {
	r.store.mediaMu.Lock()
	defer r.store.mediaMu.Unlock()
	err := r.store.SaveMediaJob(ctx, job)
	if err == nil {
		delete(r.store.mediaPending, r)
	}
	return err
}
