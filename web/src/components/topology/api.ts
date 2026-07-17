export type TopologyModelUsage = {
  provider: string;
  credential_id: string;
  account_label: string;
  model: string;
  request_count: number;
  last_used_at: string;
};

export type TopologyClient = {
  client_key_id: string;
  client_label: string;
  total_requests: number;
  today_requests: number;
  last_seen_at: string;
  first_seen_at: string;
  providers: string[];
  model_usage: TopologyModelUsage[];
};

export type TopologyClientDetail = {
  client_key_id: string;
  client_label: string;
  summary: {
    total_requests: number;
    today_requests: number;
    last_seen_at: string;
    first_seen_at: string;
  };
  models: Array<{
    model: string;
    provider: string;
    credential_id: string;
    account_label: string;
    request_count: number;
    last_used_at: string;
  }>;
};

async function adminFetch<T>(secret: string, path: string) {
  const response = await fetch(path, {
    headers: secret ? { Authorization: `Bearer ${secret}` } : {},
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data as T;
}

export function fetchTopologyClients(secret: string) {
  return adminFetch<TopologyClient[]>(secret, "/api/admin/topology/clients");
}

export function fetchTopologyClientDetail(secret: string, clientKeyId: string) {
  return adminFetch<TopologyClientDetail>(
    secret,
    `/api/admin/topology/clients/${encodeURIComponent(clientKeyId)}`,
  );
}
