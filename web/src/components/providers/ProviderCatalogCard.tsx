import { Link } from "react-router-dom";
import { Badge } from "../ui";
import type { ProviderTypeInfo } from "./catalog";
import { getProviderStats, type Credential, type Provider } from "./types";

type Props = {
  catalog: ProviderTypeInfo;
  instances: Provider[];
  credentials: Record<string, Credential[]>;
  to: string;
};

/** Compact provider tile — matches 9router ProviderCard layout. */
export function ProviderCatalogCard({ catalog, instances, credentials, to }: Props) {
  const allCreds = instances.flatMap((p) => credentials[p.ID] || []);
  const stats = getProviderStats(allCreds);
  const allDisabled = instances.length > 0 && instances.every((p) => !p.Enabled);
  const dimmed = allDisabled || (instances.length === 1 && !instances[0]?.Enabled);

  return (
    <Link
      to={to}
      className={`provider-catalog-card ${dimmed ? "disabled" : ""}`}
    >
      <div className="provider-catalog-card-inner">
        <span
          className="provider-catalog-icon"
          style={{ backgroundColor: `${catalog.color}22`, color: catalog.color }}
          aria-hidden
        >
          <span className="material-symbols-outlined">{catalog.icon}</span>
        </span>
        <div className="provider-catalog-copy">
          <h3 className="provider-catalog-name">{catalog.name}</h3>
          <div className="provider-catalog-status">
            <ProviderStatus catalog={catalog} stats={stats} allDisabled={allDisabled} instances={instances} />
          </div>
        </div>
      </div>
    </Link>
  );
}

function ProviderStatus({
  catalog,
  stats,
  allDisabled,
  instances,
}: {
  catalog: ProviderTypeInfo;
  stats: ReturnType<typeof getProviderStats>;
  allDisabled: boolean;
  instances: Provider[];
}) {
  if (allDisabled) {
    return (
      <Badge variant="default" size="sm">
        <span className="badge-with-icon">
          <span className="material-symbols-outlined">pause_circle</span>
          Disabled
        </span>
      </Badge>
    );
  }
  if (catalog.noAuth && instances.some((p) => p.Enabled)) {
    return (
      <Badge variant="success" size="sm" dot>
        Ready
      </Badge>
    );
  }
  if (stats.connected > 0) {
    return (
      <Badge variant="success" size="sm" dot>
        {stats.connected} Connected
      </Badge>
    );
  }
  if (stats.error > 0) {
    return (
      <Badge variant="error" size="sm" dot>
        {stats.error} Error
      </Badge>
    );
  }
  return <span className="provider-no-connections">No connections</span>;
}

type CustomCardProps = {
  provider: Provider;
  credentials: Credential[];
  to: string;
};

/** Card for a configured OpenAI/Anthropic compatible upstream instance. */
export function CustomProviderCard({ provider, credentials, to }: CustomCardProps) {
  const stats = getProviderStats(credentials);
  const dimmed = !provider.Enabled;

  return (
    <Link to={to} className={`provider-catalog-card ${dimmed ? "disabled" : ""}`}>
      <div className="provider-catalog-card-inner">
        <span className="provider-catalog-icon provider-catalog-icon-custom" aria-hidden>
          <span className="material-symbols-outlined">extension</span>
        </span>
        <div className="provider-catalog-copy">
          <h3 className="provider-catalog-name">{provider.Name || provider.ID}</h3>
          <div className="provider-catalog-status">
            {!provider.Enabled ? (
              <Badge variant="default" size="sm">
                Disabled
              </Badge>
            ) : stats.connected > 0 ? (
              <Badge variant="success" size="sm" dot>
                {stats.connected} Connected
              </Badge>
            ) : (
              <span className="provider-no-connections">No connections</span>
            )}
          </div>
        </div>
      </div>
    </Link>
  );
}
