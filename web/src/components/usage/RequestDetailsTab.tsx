import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge, Button, Card, Modal, Select } from "../ui";
import { fetchUsageEvents } from "./api";
import type { UsageEvent } from "./api";
import { fmt, fmtCost } from "./utils";

type ProviderItem = {
  id: string;
  name: string;
};

type Props = {
  secret: string;
  providers: ProviderItem[];
  providerNames: Record<string, string>;
  onError: (message: string) => void;
};

const PAGE_SIZE = 20;

export function RequestDetailsTab({ secret, providers, providerNames, onError }: Props) {
  const [events, setEvents] = useState<UsageEvent[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [providerFilter, setProviderFilter] = useState("");
  const [selected, setSelected] = useState<UsageEvent | null>(null);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await fetchUsageEvents(secret, {
        limit: PAGE_SIZE,
        offset: (page - 1) * PAGE_SIZE,
        providerId: providerFilter || undefined,
      });
      setEvents(result.data);
      setTotal(result.total);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Failed to load usage events");
    } finally {
      setLoading(false);
    }
  }, [secret, page, providerFilter, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  const providerOptions = useMemo(
    () => providers.map((provider) => ({ id: provider.id, name: providerNames[provider.id] || provider.name || provider.id })),
    [providers, providerNames],
  );

  return (
    <div className="usage-details">
      <Card pad="md">
        <div className="usage-details-filters">
          <label>
            <span>Provider</span>
            <Select value={providerFilter} onChange={(event) => { setProviderFilter(event.target.value); setPage(1); }}>
              <option value="">All providers</option>
              {providerOptions.map((provider) => (
                <option key={provider.id} value={provider.id}>{provider.name}</option>
              ))}
            </Select>
          </label>
          <Button variant="ghost" onClick={() => { setProviderFilter(""); setPage(1); }} disabled={!providerFilter}>
            Clear filters
          </Button>
        </div>
      </Card>

      <Card pad="none" className="usage-table-card">
        <div className="usage-table-scroll">
          <table className="usage-table details">
            <thead>
              <tr>
                <th>Timestamp</th>
                <th>Model</th>
                <th>Provider</th>
                <th className="right">Input</th>
                <th className="right">Saved</th>
                <th className="right">Output</th>
                <th>Latency</th>
                <th>Status</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={9} className="empty">
                    <span className="material-symbols-outlined animate-spin">progress_activity</span> Loading...
                  </td>
                </tr>
              ) : events.length === 0 ? (
                <tr>
                  <td colSpan={9} className="empty">No usage events found</td>
                </tr>
              ) : (
                events.map((event) => (
                  <tr key={`${event.request_id}-${event.created_at}`}>
                    <td>{new Date(event.created_at).toLocaleString()}</td>
                    <td><code>{event.public_model_id || event.upstream_model}</code></td>
                    <td>{providerNames[event.provider_id] || event.provider_id}</td>
                    <td className="right">{fmt(event.input_tokens)}</td>
                    <td className="right">{event.tokens_saved ? fmt(event.tokens_saved) : "—"}</td>
                    <td className="right">{fmt(event.output_tokens)}</td>
                    <td>{event.latency_ms} ms</td>
                    <td>
                      {event.status >= 200 && event.status < 400
                        ? <Badge variant="success" size="sm">{event.status}</Badge>
                        : <Badge variant="error" size="sm">{event.status || "error"}</Badge>}
                    </td>
                    <td>
                      <Button variant="outline" size="sm" onClick={() => setSelected(event)}>Detail</Button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        {!loading && total > 0 ? (
          <div className="usage-pagination">
            <Button variant="ghost" size="sm" disabled={page <= 1} onClick={() => setPage((value) => value - 1)}>Previous</Button>
            <span>Page {page} of {totalPages}</span>
            <Button variant="ghost" size="sm" disabled={page >= totalPages} onClick={() => setPage((value) => value + 1)}>Next</Button>
          </div>
        ) : null}
      </Card>

      <Modal open={!!selected} onClose={() => setSelected(null)} title="Request details" size="lg">
        {selected ? (
          <div className="usage-detail-grid">
            <div><span>Request ID</span><code>{selected.request_id}</code></div>
            <div><span>Timestamp</span>{new Date(selected.created_at).toLocaleString()}</div>
            <div><span>Provider</span>{providerNames[selected.provider_id] || selected.provider_id}</div>
            <div><span>Public model</span><code>{selected.public_model_id}</code></div>
            <div><span>Upstream model</span><code>{selected.upstream_model}</code></div>
            <div><span>Credential</span><code>{selected.credential_id || "—"}</code></div>
            <div><span>API key</span><code>{selected.client_api_key_id || "local"}</code></div>
            <div><span>Attempt</span>{selected.attempt}</div>
            <div><span>Status</span>{selected.status}</div>
            <div><span>Latency</span>{selected.latency_ms} ms</div>
            <div><span>Input tokens</span>{fmt(selected.input_tokens)}</div>
            <div><span>Output tokens</span>{fmt(selected.output_tokens)}</div>
            <div><span>Reasoning tokens</span>{fmt(selected.reasoning_tokens)}</div>
            <div><span>Tokens saved</span>{fmt(selected.tokens_saved)}</div>
            <div><span>Estimated cost</span>{fmtCost(selected.estimated_cost_usd)}</div>
            <div><span>Error code</span><code>{selected.error_code || "ok"}</code></div>
          </div>
        ) : null}
      </Modal>
    </div>
  );
}
