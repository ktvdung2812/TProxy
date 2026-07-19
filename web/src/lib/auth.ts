const STORAGE_KEY = "tproxy-management-secret";

export function getStoredManagementSecret(): string {
  return localStorage.getItem(STORAGE_KEY) || "";
}

export function setStoredManagementSecret(secret: string) {
  localStorage.setItem(STORAGE_KEY, secret);
}

export function clearStoredManagementSecret() {
  localStorage.removeItem(STORAGE_KEY);
}

export async function validateManagementSecret(secret: string): Promise<{ ok: true } | { ok: false; message: string }> {
  try {
    const response = await fetch("/api/admin/snapshot", {
      headers: secret ? { Authorization: `Bearer ${secret}` } : {},
    });
    if (response.ok) {
      return { ok: true };
    }
    const data = await response.json().catch(() => ({}));
    const code = data?.error?.code;
    if (code === "invalid_management_secret") {
      return { ok: false, message: "Mật khẩu không đúng." };
    }
    if (code === "management_secret_required") {
      return { ok: false, message: "Server yêu cầu mật khẩu quản trị." };
    }
    if (code === "management_remote_disabled") {
      return { ok: false, message: "Quản trị từ xa đã tắt. Truy cập qua localhost." };
    }
    return { ok: false, message: data?.error?.message || `HTTP ${response.status}` };
  } catch {
    return { ok: false, message: "Không kết nối được tới gateway." };
  }
}
