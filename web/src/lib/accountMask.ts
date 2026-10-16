const STORAGE_KEY = "tproxy.maskAccounts";
const CHANGE_EVENT = "tproxy:account-mask-changed";

let enabled = readStored();

function readStored(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function isAccountMaskEnabled(): boolean {
  return enabled;
}

export function setAccountMaskEnabled(value: boolean) {
  enabled = value;
  try {
    window.localStorage.setItem(STORAGE_KEY, value ? "1" : "0");
  } catch {
    // Storage unavailable — keep the in-memory flag.
  }
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

export function subscribeAccountMask(listener: () => void): () => void {
  const onStorage = (event: StorageEvent) => {
    if (event.key === STORAGE_KEY || event.key === null) {
      enabled = readStored();
      listener();
    }
  };
  window.addEventListener(CHANGE_EVENT, listener);
  window.addEventListener("storage", onStorage);
  return () => {
    window.removeEventListener(CHANGE_EVENT, listener);
    window.removeEventListener("storage", onStorage);
  };
}

function maskToken(token: string): string {
  if (token.length <= 2) return `${token.slice(0, 1)}***`;
  return `${token.slice(0, 2)}***`;
}

/**
 * Obscures an account identity for screen sharing: "someone@gmail.com" ->
 * "so***@gmail.com", "work account" -> "wo***", "cred_01hx" -> "cr***".
 */
export function maskAccountText<T extends string | null | undefined>(value: T, masked: boolean): T {
  if (!value || !masked) return value;
  const trimmed = value.trim();
  if (!trimmed) return value;
  const at = trimmed.indexOf("@");
  return (at > 0
    ? `${maskToken(trimmed.slice(0, at))}${trimmed.slice(at)}`
    : maskToken(trimmed)) as T;
}
