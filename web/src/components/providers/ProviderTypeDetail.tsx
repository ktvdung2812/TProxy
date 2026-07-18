import { Badge, Button, Card, EmptyState } from "../ui";
import type { ProviderTypeInfo } from "./catalog";
import type { ConnectionMethod, ConnectionProfile } from "./connectionMethods";
import { ProviderConnectionActions } from "./ProviderConnectionActions";
import { ProviderLogo } from "./ProviderLogo";

type Props = {
  catalog: ProviderTypeInfo;
  connectionProfile: ConnectionProfile;
  onBack: () => void;
  onSetup: () => void;
  onConnectionMethod: (method: ConnectionMethod) => void;
  connectBusy?: boolean;
};

/** Detail page for a catalog provider type with no configured instance yet. */
export function ProviderTypeDetail({
  catalog,
  connectionProfile,
  onBack,
  onSetup,
  onConnectionMethod,
  connectBusy,
}: Props) {
  return (
    <div>
      <button className="detail-back" type="button" onClick={onBack}>
        <span className="material-symbols-outlined">arrow_back</span>
        Back to providers
      </button>

      <div className="detail-header">
        <ProviderLogo
          className="provider-logo"
          providerId={catalog.presetId}
          providerType={catalog.type}
          style={{ color: catalog.color }}
          alt={`${catalog.name} logo`}
        />
        <div className="detail-title-block">
          <h2>{catalog.name}</h2>
          <div className="detail-meta">
            <Badge variant="primary" size="sm">{catalog.type}</Badge>
            <Badge variant="default" size="sm">Not configured</Badge>
          </div>
          <p className="detail-description">{catalog.description}</p>
          <div className="detail-meta" style={{ marginTop: 8 }}>
            {catalog.defaultBaseUrl && <span className="detail-url">{catalog.defaultBaseUrl}</span>}
            {catalog.website && (
              <a className="detail-link" href={catalog.website} target="_blank" rel="noreferrer">
                Website <span className="material-symbols-outlined" style={{ fontSize: 14 }}>open_in_new</span>
              </a>
            )}
            {catalog.apiKeyUrl && (
              <a className="detail-link" href={catalog.apiKeyUrl} target="_blank" rel="noreferrer">
                Get API key <span className="material-symbols-outlined" style={{ fontSize: 14 }}>key</span>
              </a>
            )}
          </div>
        </div>
        <div className="detail-header-actions">
          <Button variant="outline" size="md" icon="tune" onClick={onSetup}>
            Advanced setup
          </Button>
        </div>
      </div>

      <Card pad="md" className="section" title="Connections" icon="vpn_key">
        <ProviderConnectionActions
          profile={connectionProfile}
          onMethod={onConnectionMethod}
          busy={connectBusy}
          size="md"
        />
        <EmptyState
          icon="key_off"
          text="No connections yet."
          hint="Pick a connection method above. tproxy will create the provider automatically when you connect."
        />
      </Card>
    </div>
  );
}
