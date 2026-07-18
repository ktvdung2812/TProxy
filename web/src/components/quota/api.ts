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
