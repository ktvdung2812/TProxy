import type { ChatAttachment, ChatMessage } from "./types";

export const STORAGE_KEYS = {
  sessions: "tproxy-chat.sessions",
  activeSessionId: "tproxy-chat.activeSessionId",
  activeModelId: "tproxy-chat.activeModelId",
  draft: "tproxy-chat.draft",
  apiKey: "tproxy-api-key",
} as const;

export function createId(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  return `chat_${Date.now()}_${Math.random().toString(16).slice(2)}`;
}

export function safeParse<T>(value: string | null, fallback: T): T {
  if (!value) return fallback;
  try {
    return JSON.parse(value) as T;
  } catch {
    return fallback;
  }
}

export function textValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value == null) return "";
  if (Array.isArray(value)) return value.map(textValue).filter(Boolean).join(" ");
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    if (typeof record.message === "string") return record.message;
    if (typeof record.error === "string") return record.error;
    try {
      return JSON.stringify(value);
    } catch {
      return String(value);
    }
  }
  return String(value);
}

export function makeSessionTitle(text = ""): string {
  const normalized = textValue(text).replace(/\s+/g, " ").trim();
  if (!normalized) return "New chat";
  return normalized.length > 52 ? `${normalized.slice(0, 52).trimEnd()}…` : normalized;
}

export function formatRelativeTime(value?: string): string {
  if (!value) return "Now";
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return "Now";
  const diffMinutes = Math.max(1, Math.round((Date.now() - time) / 60000));
  if (diffMinutes < 60) return `${diffMinutes}m`;
  const diffHours = Math.round(diffMinutes / 60);
  if (diffHours < 24) return `${diffHours}h`;
  return `${Math.round(diffHours / 24)}d`;
}

export function buildUserContent(message: Pick<ChatMessage, "content" | "attachments">): string | Array<{ type: string; text?: string; image_url?: { url: string } }> {
  const text = textValue(message.content).trim();
  const attachments = Array.isArray(message.attachments) ? message.attachments : [];

  if (attachments.length === 0) return text;

  const content: Array<{ type: string; text?: string; image_url?: { url: string } }> = [];
  if (text) content.push({ type: "text", text });

  for (const attachment of attachments) {
    if (attachment?.dataUrl) {
      content.push({ type: "image_url", image_url: { url: attachment.dataUrl } });
    }
  }

  return content.length > 0 ? content : text;
}

export function readAssistantText(chunk: unknown): string {
  if (!chunk || typeof chunk !== "object") return "";
  const record = chunk as {
    choices?: Array<{ delta?: { content?: unknown }; message?: { content?: unknown } }>;
    output_text?: unknown;
    text?: unknown;
  };
  const choice = record.choices?.[0];
  const delta = choice?.delta || {};
  const pieces = [delta.content, choice?.message?.content, record.output_text, record.text]
    .map(textValue)
    .filter(Boolean);
  return pieces[0] || "";
}

export async function fileToDataUrl(file: File): Promise<string> {
  return await new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error || new Error("Failed to read file"));
    reader.readAsDataURL(file);
  });
}

export function cloneSession<T extends { messages: ChatMessage[] }>(session: T): T {
  return {
    ...session,
    messages: session.messages.map((message) => ({ ...message })),
  };
}
