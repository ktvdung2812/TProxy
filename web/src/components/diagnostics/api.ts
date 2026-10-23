import type { SourceProtocol, TargetProtocol } from "./protocols";

export type CanonicalPreview = {
  source?: string;
  public_model_id?: string;
  upstream_model?: string;
  messages?: unknown[];
  system?: unknown;
  tools?: unknown[];
  max_tokens?: number;
  stream?: boolean;
  [key: string]: unknown;
};

export type TranslationPreview = {
  canonical: CanonicalPreview;
  outbound: unknown;
  offline?: boolean;
  notes?: string[];
};

/** Offline protocol conversion: the server never contacts an upstream for this. */
export async function previewTranslation(
  secret: string,
  payload: { source: SourceProtocol; target: TargetProtocol; model: string; body: Record<string, unknown> },
): Promise<TranslationPreview> {
  const response = await fetch("/api/admin/diagnostics/translate", {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(secret ? { Authorization: `Bearer ${secret}` } : {}) },
    body: JSON.stringify(payload),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data?.error?.message || `HTTP ${response.status}`);
  return data as TranslationPreview;
}
