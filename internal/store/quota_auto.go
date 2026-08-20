package store

import "context"

// SyncCredentialQuotaState temporarily disables credentials at 0% quota and restores
// them automatically once upstream quota recovers. Manual disables are left alone.
func (s *Store) SyncCredentialQuotaState(ctx context.Context, credential Credential, depleted bool) (bool, error) {	autoDisabled := QuotaAutoDisabled(credential.Metadata)

	switch {
	case depleted && credential.Enabled:
		if err := s.SetCredentialEnabled(ctx, credential.ID, false); err != nil {
			return false, err
		}
		if err := s.UpdateCredentialMetadata(ctx, credential.ID, quotaAutoDisabledMetadata(credential.Metadata, true)); err != nil {
			return false, err
		}
		return true, nil
	case !depleted && !credential.Enabled && autoDisabled:
		if err := s.SetCredentialEnabled(ctx, credential.ID, true); err != nil {
			return false, err
		}
		if err := s.UpdateCredentialMetadata(ctx, credential.ID, quotaAutoDisabledMetadata(credential.Metadata, false)); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

// SyncCredentialRenewal persists the subscription renewal date reported by the
// latest quota probe, so routing strategies can prefer accounts that renew
// soonest. Metadata is rewritten only when the value actually changed.
func (s *Store) SyncCredentialRenewal(ctx context.Context, credentialID, renewsAt string) error {
	credential, err := s.CredentialByID(ctx, credentialID)
	if err != nil {
		return err
	}
	current := ""
	if existing, ok := credential.Metadata[quotaRenewsAtKey].(string); ok {
		current = existing
	}
	if current == renewsAt {
		return nil
	}
	return s.UpdateCredentialMetadata(ctx, credentialID, quotaRenewsAtMetadata(credential.Metadata, renewsAt))
}
