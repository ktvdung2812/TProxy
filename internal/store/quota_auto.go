package store

import (
	"context"
	"time"
)

// SyncCredentialQuotaState temporarily disables credentials at 0% quota and restores
// them automatically once upstream quota recovers. Manual disables are left alone.
func (s *Store) SyncCredentialQuotaState(ctx context.Context, credential Credential, depleted bool) (bool, error) {
	var result interface{ RowsAffected() (int64, error) }
	var err error
	if depleted {
		// Compare current state in SQL: a stale quota probe cannot take ownership
		// of an account the operator disabled while the probe was in flight.
		result, err = s.db.ExecContext(ctx, `UPDATE credentials SET enabled=0,metadata_json=json_set(metadata_json,'$.quota_auto_disabled',json('true'),'$.quota_auto_disabled_at',?) WHERE id=? AND enabled=1`, time.Now().UTC().Format(time.RFC3339Nano), credential.ID)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE credentials SET enabled=1,metadata_json=json_remove(metadata_json,'$.quota_auto_disabled','$.quota_auto_disabled_at') WHERE id=? AND enabled=0 AND json_extract(metadata_json,'$.quota_auto_disabled') IN (1,'true','1')`, credential.ID)
	}
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// SyncCredentialRenewal persists the subscription renewal date reported by the
// latest quota probe, so routing strategies can prefer accounts that renew
// soonest. Metadata is rewritten only when the value actually changed.
func (s *Store) SyncCredentialRenewal(ctx context.Context, credentialID, renewsAt string) error {
	var err error
	if renewsAt == "" {
		_, err = s.db.ExecContext(ctx, `UPDATE credentials SET metadata_json=json_remove(metadata_json,'$.quota_renews_at') WHERE id=? AND json_extract(metadata_json,'$.quota_renews_at') IS NOT NULL`, credentialID)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE credentials SET metadata_json=json_set(metadata_json,'$.quota_renews_at',?) WHERE id=? AND COALESCE(json_extract(metadata_json,'$.quota_renews_at'),'')<>?`, renewsAt, credentialID, renewsAt)
	}
	return err
}
