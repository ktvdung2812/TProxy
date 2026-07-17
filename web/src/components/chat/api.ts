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
    const errorData = await response.json().catch(() => ({}));
    const record = errorData as { error?: { message?: string } | string; message?: string };
    const errorMessage =
      typeof record.error === "string"
        ? record.error
        : record.error?.message || record.message || `Request failed (${response.status})`;
    throw new Error(errorMessage);
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

      try {
        const chunk = JSON.parse(payload);
        const text = readAssistantText(chunk);
        if (!text) continue;
        assistantText += text;
        callbacks.onDelta(assistantText);
      } catch {
        // Ignore malformed chunks.
      }
    }
  }

  return assistantText;
}

function textValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value == null) return "";
  return String(value);
}
