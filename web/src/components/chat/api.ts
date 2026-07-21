import { readAssistantText } from "./utils";

type ChatCompletionMessage = {
  role: string;
  content: unknown;
};

type StreamCallbacks = {
  onDelta: (text: string) => void;
  signal?: AbortSignal;
};

export async function streamChatCompletion(
  apiKey: string,
  model: string,
  messages: ChatCompletionMessage[],
  callbacks: StreamCallbacks,
): Promise<string> {
  const response = await fetch("/v1/chat/completions", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "text/event-stream",
      ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}),
    },
    body: JSON.stringify({ model, messages, stream: true }),
    signal: callbacks.signal,
  });

  if (!response.ok) {
    throw new Error(await readErrorMessage(response));
  }

  const reader = response.body?.getReader();
  if (!reader) {
    const data = await response.json().catch(() => ({}));
    const record = data as { choices?: Array<{ message?: { content?: unknown } }>; output_text?: unknown };
    return textValue(record?.choices?.[0]?.message?.content || record?.output_text || "");
  }

  const decoder = new TextDecoder();
  let buffer = "";
  let assistantText = "";

  while (true) {
    const { value, done } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split(/\r?\n/);
    buffer = lines.pop() || "";

    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed.startsWith("data:")) continue;

      const payload = trimmed.slice(5).trim();
      if (!payload || payload === "[DONE]") continue;

      let chunk: { error?: { message?: string } | string };
      try {
        chunk = JSON.parse(payload) as { error?: { message?: string } | string };
      } catch {
        continue;
      }
      if (chunk.error) {
        const message =
          typeof chunk.error === "string"
            ? chunk.error
            : chunk.error.message || `Request failed (${response.status})`;
        throw new Error(message);
      }
      const text = readAssistantText(chunk);
      if (!text) continue;
      assistantText += text;
      callbacks.onDelta(assistantText);
    }
  }

  return assistantText;
}

async function readErrorMessage(response: Response): Promise<string> {
  const raw = await response.text().catch(() => "");
  const trimmed = raw.trim();
  if (!trimmed) return `Request failed (${response.status})`;

  // Auth and other pre-stream failures may be JSON or OpenAI SSE (`data: {...}`).
  const candidates = [trimmed];
  for (const line of trimmed.split(/\r?\n/)) {
    const value = line.trim();
    if (value.startsWith("data:")) {
      const payload = value.slice(5).trim();
      if (payload && payload !== "[DONE]") candidates.push(payload);
    }
  }

  for (const candidate of candidates) {
    try {
      const record = JSON.parse(candidate) as {
        error?: { message?: string } | string;
        message?: string;
      };
      if (typeof record.error === "string" && record.error.trim()) return record.error;
      if (record.error && typeof record.error === "object" && record.error.message?.trim()) {
        return record.error.message;
      }
      if (record.message?.trim()) return record.message;
    } catch {
      // try next candidate
    }
  }

  return `Request failed (${response.status})`;
}

function textValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value == null) return "";
  return String(value);
}
