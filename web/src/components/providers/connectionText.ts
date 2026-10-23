import type { TFunction } from "i18next";
import type { ConnectionMethod, ConnectionProfile } from "./connectionMethods";

/* Translations for the copy produced by resolveConnectionProfile. The
   resolver stays locale-free (it is plain data shared by several views), so
   its fixed English strings are mapped to keys here. Text that comes from the
   9router registry (auth hints) is upstream data and passes through as is. */

const LABEL_KEYS: Record<string, string> = {
  "Grok CLI Device Login": "providers.method.grokCliDevice",
  "GitHub OAuth": "providers.method.githubOAuth",
  "Google Sign-In": "providers.method.googleSignIn",
  "GitHub Device Login": "providers.method.githubDevice",
  "Add Ollama Host": "providers.method.addOllamaHost",
  "Add Cookie": "providers.method.addCookie",
  "Add API Key": "providers.method.addApiKey",
  "Add Service Account": "providers.method.addServiceAccount",
  "Add no-auth connection": "providers.method.addNoAuth",
  "Connect Cursor IDE": "providers.method.connectCursor",
  "Connect Kiro": "providers.method.connectKiro",
  "Import data": "import.cardTitle",
};

const DESCRIPTION_KEYS: Record<string, string> = {
  "Use proxy pools and routing without upstream credentials.": "providers.method.desc.noAuth",
  "Auto-detect access token and machine ID from Cursor's local SQLite database.": "providers.method.desc.cursor",
  "Select JSON files; the format is detected and converted automatically.": "providers.method.desc.importData",
  "AWS Builder ID, IAM Identity Center, API key, token import, or CLIProxyAPI JSON.": "providers.method.desc.kiro",
  "Paste the session cookie from your browser.": "providers.method.desc.cookie",
  "Device authorization — complete sign-in in the browser tab that opens.": "providers.method.desc.oauthDevice",
  "Browser OAuth — sign in with your provider account.": "providers.method.desc.oauthBrowser",
  "Paste your API key and provider base URL.": "providers.method.desc.byoKeyNoUrl",
  "Set the Ollama host URL. API key is optional for local instances.": "providers.method.desc.ollamaHost",
  "Paste an API key from the provider console.": "providers.method.desc.apiKey",
  "Paste Google Cloud service account JSON.": "providers.method.desc.serviceAccount",
};

const NOTICE_KEYS: Record<string, string> = {
  "This provider works without API credentials. Add a no-auth connection or enable the provider to route traffic.": "providers.method.notice.noAuth",
  "Open Cursor IDE and sign in, then import the access token and machine ID from state.vscdb.": "providers.method.notice.cursor",
  "Choose AWS Builder ID, IAM Identity Center, API key, or import a token from Kiro IDE.": "providers.method.notice.kiro",
  "Interactive OAuth is not available for this provider yet. Use Import data if you already have exported credentials.": "providers.method.notice.oauthUnavailable",
};

export function methodText(method: ConnectionMethod, t: TFunction): string {
  const key = LABEL_KEYS[method.label];
  if (key) return t(key);
  const signIn = method.label.match(/^Sign in with (.+)$/);
  if (signIn) return t("providers.method.signInWith", { name: signIn[1] });
  return method.label;
}

/** Short label for the account toolbar: OAuth sign-ins read as "Add account". */
export function methodShortText(method: ConnectionMethod, t: TFunction): string {
  return method.kind === "oauth" ? t("providers.method.addAccount") : methodText(method, t);
}

export function methodDescription(method: ConnectionMethod, t: TFunction): string | undefined {
  const text = method.description;
  if (!text) return undefined;
  const key = DESCRIPTION_KEYS[text];
  if (key) return t(key);
  const byoKey = text.match(/^Paste your API key and use the provider base URL \(for example (.+)\)\.$/);
  if (byoKey) return t("providers.method.desc.byoKey", { url: byoKey[1] });
  return text;
}

export function profileNotice(profile: ConnectionProfile, t: TFunction): string | undefined {
  if (!profile.notice) return undefined;
  const key = NOTICE_KEYS[profile.notice];
  return key ? t(key) : profile.notice;
}

export function methodIcon(kind: ConnectionMethod["kind"]): string {
  switch (kind) {
    case "oauth":
      return "login";
    case "api_key":
      return "key";
    case "cookie":
      return "cookie";
    case "service_account":
      return "badge";
    case "none":
      return "lock_open";
    case "import_data":
      return "upload_file";
    case "import_cursor":
      return "edit";
    case "connect_kiro":
      return "shield";
    default: {
      const _exhaustive: never = kind;
      return _exhaustive;
    }
  }
}
