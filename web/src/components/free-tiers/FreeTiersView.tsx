import { useCallback, useEffect, useState } from "react";
import { Badge, Card } from "../ui";

type FreeTierItem = {
  provider_id: string;
  name: string;
  category: string;
  models?: string[];
  daily_limit?: string;
  reset_window?: string;
  auth_type: string;
  api_key_url?: string;
  has_oauth: boolean;
  notes?: string;
  configured: boolean;
};

type Props = {
  secret: string;
  onError: (message: string) => void;
};

async function fetchFreeTiers(secret: string) {
  const response = await fetch("/api/admin/free-tiers", {
    headers: secret ? { Authorization: `Bearer ${secret}` } : {},
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data as { items: FreeTierItem[]; strategies: string[] };
}

export function FreeTiersView({ secret, onError }: Props) {
  const [items, setItems] = useState<FreeTierItem[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await fetchFreeTiers(secret);
      setItems(data.items || []);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to load free-tier catalog");
    } finally {
      setLoading(false);
    }
  }, [secret, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <section className="section free-tiers-page">
      <Card pad="md">
        <div className="page-head-inline">
          <div>
            <h2>Free-tier catalog</h2>
            <p className="muted">Aggregator view of zero-cost provider presets and configured status.</p>
          </div>
          <Badge variant="neutral" size="sm">{items.length} providers</Badge>
        </div>
        {loading ? (
          <p className="muted">Loading catalog…</p>
        ) : (
          <div className="free-tier-grid">
            {items.map((item) => (
              <article key={item.provider_id} className="free-tier-card">
                <div className="free-tier-card-head">
                  <h3>{item.name}</h3>
                  <Badge variant={item.configured ? "success" : "neutral"} size="sm">
                    {item.configured ? "Configured" : "Not configured"}
                  </Badge>
                </div>
                <p className="muted">{item.provider_id} · {item.auth_type}</p>
                {item.daily_limit && <p><strong>Limit:</strong> {item.daily_limit}</p>}
                {item.reset_window && <p><strong>Reset:</strong> {item.reset_window}</p>}
                {item.models && item.models.length > 0 && (
                  <p><strong>Models:</strong> {item.models.join(", ")}</p>
                )}
                {item.notes && <p className="muted">{item.notes}</p>}
                {item.api_key_url && (
                  <a href={item.api_key_url} target="_blank" rel="noreferrer" className="text-link">
                    Get API key
                  </a>
                )}
              </article>
            ))}
          </div>
        )}
      </Card>
    </section>
  );
}
