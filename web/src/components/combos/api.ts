import type { ComboFormData } from "./types";
import { formToPayload } from "./utils";

async function adminFetch<T>(
  secret: string,
  path: string,
  method: "GET" | "POST" | "PUT" | "DELETE" = "GET",
  body?: unknown,
) {
  const response = await fetch(path, {
    method,
    headers: {
      ...(secret ? { Authorization: `Bearer ${secret}` } : {}),
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(data?.error?.message || data?.message || `HTTP ${response.status}`);
  }
  return data as T;
}

export function saveCombo(secret: string, form: ComboFormData, editing: boolean) {
  return adminFetch<{ ok: boolean; combo_id: string }>(
    secret,
    "/api/admin/combos",
    editing ? "PUT" : "POST",
    formToPayload(form),
  );
}

export function deleteCombo(secret: string, id: string) {
  return adminFetch<{ ok: boolean; combo_id: string }>(
    secret,
    `/api/admin/combos/${encodeURIComponent(id)}`,
    "DELETE",
  );
}
