export type FreeTierItem = {
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

export async function fetchFreeTiers(secret: string) {
  const response = await fetch("/api/admin/free-tiers", {
    headers: secret ? { Authorization: `Bearer ${secret}` } : {},
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || `HTTP ${response.status}`);
  }
  return data as { items: FreeTierItem[]; strategies: string[] };
}
