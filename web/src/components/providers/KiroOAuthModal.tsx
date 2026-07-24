import { useCallback, useState } from "react";
import { KiroAuthModal, type KiroDeviceConfig } from "./KiroAuthModal";
import { OAuthModal } from "./OAuthModal";

type Props = {
  open: boolean;
  providerId: string;
  providerType: string;
  secret: string;
  onClose: () => void;
  onComplete?: () => void;
  onError?: (message: string) => void;
};

export function KiroOAuthModal({ open, providerId, providerType, secret, onClose, onComplete, onError }: Props) {
  const [deviceConfig, setDeviceConfig] = useState<KiroDeviceConfig | null>(null);

  const handleClose = useCallback(() => {
    setDeviceConfig(null);
    onClose();
  }, [onClose]);

  const handleComplete = useCallback(() => {
    setDeviceConfig(null);
    onComplete?.();
  }, [onComplete]);

  if (deviceConfig) {
    return (
      <OAuthModal
        open={open}
        providerId={providerId}
        providerType={providerType}
        secret={secret}
        autoStart
        kiroConfig={deviceConfig}
        onClose={() => setDeviceConfig(null)}
        onComplete={handleComplete}
        onError={onError}
      />
    );
  }

  return (
    <KiroAuthModal
      open={open}
      secret={secret}
      providerId={providerId}
      onClose={handleClose}
      onDeviceFlow={setDeviceConfig}
      onComplete={handleComplete}
      onError={onError}
    />
  );
}
