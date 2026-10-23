export type SourceProtocol = "openai" | "responses" | "claude" | "gemini";
export type TargetProtocol = "openai" | "codex" | "claude" | "gemini";

/** Client-facing protocols the gateway accepts; labels are product names and stay untranslated. */
export const SOURCE_PROTOCOLS: Array<{ value: SourceProtocol; label: string; endpoint: string }> = [
  { value: "openai", label: "OpenAI Chat", endpoint: "/v1/chat/completions" },
  { value: "responses", label: "OpenAI Responses", endpoint: "/v1/responses" },
  { value: "claude", label: "Anthropic Messages", endpoint: "/v1/messages" },
  { value: "gemini", label: "Gemini", endpoint: "/v1beta/models/*:generateContent" },
];

/** Upstream wire formats the preview can build. */
export const TARGET_PROTOCOLS: Array<{ value: TargetProtocol; label: string }> = [
  { value: "openai", label: "OpenAI-compatible" },
  { value: "codex", label: "Codex · Responses" },
  { value: "claude", label: "Anthropic" },
  { value: "gemini", label: "Gemini" },
];

export const EXAMPLES: Record<SourceProtocol, unknown> = {
  openai: { model: "diagnostic-model", messages: [{ role: "user", content: "Hello" }], stream: true },
  responses: { model: "diagnostic-model", instructions: "Be concise", input: [{ role: "user", content: "Hello" }], stream: true },
  claude: { model: "diagnostic-model", system: "Be concise", max_tokens: 512, messages: [{ role: "user", content: "Hello" }] },
  gemini: { contents: [{ role: "user", parts: [{ text: "Hello" }] }], generationConfig: { maxOutputTokens: 512 } },
};

export const exampleText = (source: SourceProtocol) => JSON.stringify(EXAMPLES[source], null, 2);

/** True when the editor still holds an untouched example, so switching protocol may replace it. */
export function isPristineBody(body: string) {
  const trimmed = body.trim();
  return !trimmed || SOURCE_PROTOCOLS.some((protocol) => exampleText(protocol.value) === trimmed);
}

/** `message` is the parser's own text; an empty message means "valid JSON, but not an object". */
export type JsonCheck = { ok: true; value: Record<string, unknown> } | { ok: false; message: string };

export function checkJson(text: string): JsonCheck {
  try {
    const value = JSON.parse(text) as unknown;
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      return { ok: false, message: "" };
    }
    return { ok: true, value: value as Record<string, unknown> };
  } catch (cause) {
    return { ok: false, message: cause instanceof Error ? cause.message : String(cause) };
  }
}

export function byteSize(text: string) {
  const bytes = new TextEncoder().encode(text).length;
  return bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(bytes < 10240 ? 1 : 0)} KB`;
}
