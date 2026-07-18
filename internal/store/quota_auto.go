package store

import "context"

// SyncCredentialQuotaState temporarily disables credentials at 0% quota and restores
// them automatically once upstream quota recovers. Manual disables are left alone.
func (s *Store) SyncCredentialQuotaState(ctx context.Context, credential Credential, depleted bool) (bool, error) {
	autoDisabled := QuotaAutoDisabled(credential.Metadata)

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
