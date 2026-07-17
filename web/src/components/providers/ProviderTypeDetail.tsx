import { Badge, Button, Card, EmptyState } from "../ui";
import type { ProviderTypeInfo } from "./catalog";

type Props = {
  catalog: ProviderTypeInfo;
  onBack: () => void;
  onSetup: () => void;
  onConnect?: () => void;
  connectBusy?: boolean;
};

/** Detail page for a catalog provider type with no configured instance yet. */
export function ProviderTypeDetail({ catalog, onBack, onSetup, onConnect, connectBusy }: Props) {
  const connectLabel = `Connect ${catalog.name}`;

  return (
    <div>
      <button className="detail-back" type="button" onClick={onBack}>
        <span className="material-symbols-outlined">arrow_back</span>
        Back to providers
      </button>

      <div className="detail-header">
        <span
          className="provider-logo"
          style={{ backgroundColor: `${catalog.color}22`, color: catalog.color }}
        >
          <span className="material-symbols-outlined">{catalog.icon}</span>
        </span>
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
          {catalog.supportsOAuth && onConnect ? (
            <>
              <Button variant="primary" size="md" icon="lock_person" onClick={onConnect} loading={connectBusy}>
                {connectLabel}
              </Button>
              <Button variant="outline" size="md" icon="tune" onClick={onSetup}>
                Advanced setup
              </Button>
            </>
          ) : (
            <Button variant="primary" size="md" icon="add" onClick={onSetup}>
              Set up provider
            </Button>
          )}
        </div>
      </div>

      <Card
        pad="md"
        className="section"
        title="Connections"
        icon="vpn_key"
        action={
          catalog.supportsOAuth && onConnect ? (
            <Button variant="primary" size="sm" icon="lock_person" onClick={onConnect} loading={connectBusy}>
              {connectLabel}
            </Button>
          ) : undefined
        }
      >
        <EmptyState
          icon="key_off"
          text="No connections yet."
          hint={
            catalog.supportsOAuth
              ? `Click "${connectLabel}" to authorize your account via OAuth.`
              : "Set up this provider, then add a credential to enable it."
          }
        />
      </Card>
    </div>
  );
}
