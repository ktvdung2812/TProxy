import type { RequestLog } from "../../hooks/useRequestLogStream";

export type QuotaEntry = {
  name?: string;
  used: number;
  total: number;
  remaining?: number;
  reset_at?: string;
  unlimited?: boolean;
};

export type CredentialQuota = {
  credential_id: string;
  provider_id: string;
  provider_type: string;
  plan?: string;
  message?: string;
  reset_credits?: {
    available_count: number;
    credits?: Array<{
      status: string;
      granted_at?: string;
      expires_at?: string;
    }>;
  };
  quotas: Record<string, QuotaEntry>;
  credential_enabled?: boolean;
  quota_auto_disabled?: boolean;
};

export type CodexResetCredits = {
  available_count: number;
  credits?: Array<{
    status: string;
    granted_at?: string;
    expires_at?: string;
  }>;
};

export type CodexResetConsumeResult = {
  ok: boolean;
  reset: boolean;
  code?: string;
  windows_reset?: number;
  message?: string;
  no_credit?: boolean;
  redeem_request_id?: string;
};

export type QuotaSummary = {
  day_start: string;
  global_limits: {
    requests_per_minute?: number;
    concurrent_streams?: number;
    budget_usd_per_day?: number;
  };
  api_keys: Array<{
    id: string;
    name: string;
    enabled: boolean;
    requests_today: number;
    cost_usd_today: number;
    budget_usd_per_day?: number;
    requests_per_minute?: number;
  }>;
};

export type CredentialProxyUsage = {
  requests: number;
  promptTokens: number;
  completionTokens: number;
};

export type CredentialUsageResponse = {
  period: string;
  by_credential: Record<string, CredentialProxyUsage>;
};

async function adminFetch<T>(secret: string, path: string, method: "GET" | "POST" = "GET") {
  const response = await fetch(path, {
    method,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
    },
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data as T;
}

export function fetchQuotaSummary(secret: string) {
  return adminFetch<QuotaSummary>(secret, "/api/admin/quota/summary");
}

export function fetchCredentialProxyUsage(secret: string, period: "all" | "today" | "24h" = "all") {
  return adminFetch<CredentialUsageResponse>(
    secret,
    `/api/admin/quota/credential-usage?period=${encodeURIComponent(period)}`,
  );
}

export function fetchCredentialQuota(secret: string, credentialId: string) {
  return adminFetch<CredentialQuota>(secret, `/api/admin/credentials/${encodeURIComponent(credentialId)}/quota`, "POST");
}

export function fetchCredentialRequestLogs(secret: string, credentialId: string, limit = 100) {
  return adminFetch<{ credential_id: string; data: RequestLog[] }>(
    secret,
    `/api/admin/credentials/${encodeURIComponent(credentialId)}/logs?limit=${limit}`,
  );
}

export function fetchCodexResetCredits(secret: string, credentialId: string) {
  return adminFetch<CodexResetCredits>(
    secret,
    `/api/admin/credentials/${encodeURIComponent(credentialId)}/codex-reset-credits`,
    "GET",
  );
}

export function consumeCodexResetCredit(secret: string, credentialId: string) {
  return fetch(`/api/admin/credentials/${encodeURIComponent(credentialId)}/codex-reset-credits`, {
    method: "POST",
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
    },
  }).then(async (response) => {
    const data = (await response.json().catch(() => ({}))) as CodexResetConsumeResult & {
      error?: { message?: string };
    };
    if (response.status === 409 && data.no_credit) {
      return data;
    }
    if (!response.ok) {
      throw new Error(data?.error?.message || data?.message || `HTTP ${response.status}`);
    }
    return data;
  });
}

export type AccountChatMessage = { role: "user" | "assistant"; content: string };

export type AccountChatResult = {
  ok: boolean;
  content?: string;
  reasoning?: string;
  model?: string;
  latency_ms: number;
  error?: string;
  status?: number;
  usage?: { input_tokens?: number; output_tokens?: number };
};

// Runs one exchange against this account specifically. The backend pins the
// credential instead of routing, so a failure here belongs to this account and
// is not masked by failover to a healthy sibling.
export function sendAccountChat(
  secret: string,
  credentialId: string,
  model: string,
  messages: AccountChatMessage[],
) {
  return fetch(`/api/admin/credentials/${encodeURIComponent(credentialId)}/chat`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
    },
    body: JSON.stringify({ model, messages }),
  }).then(async (response) => {
    // A pinned-credential failure comes back 200 with ok:false and a plain
    // error string; only transport and auth failures use the envelope shape.
    const data = (await response.json().catch(() => ({}))) as Omit<AccountChatResult, "error"> & {
      error?: { message?: string } | string;
    };
    if (!response.ok) {
      const detail = typeof data.error === "string" ? data.error : data.error?.message;
      throw new Error(detail || `HTTP ${response.status}`);
    }
    return {
      ...data,
      error: typeof data.error === "string" ? data.error : data.error?.message,
    } as AccountChatResult;
  });
}
