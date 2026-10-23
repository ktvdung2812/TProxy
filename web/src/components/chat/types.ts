export type ChatAttachment = {
  id: string;
  name: string;
  type: string;
  dataUrl: string;
};

export type ChatMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  attachments?: ChatAttachment[];
  createdAt: string;
  status?: "streaming" | "done" | "error";
  /** Model that produced an assistant message (sessions can switch models). */
  modelName?: string;
  /** Set when the user stopped the stream; the partial answer is kept. */
  stopped?: boolean;
  /** Time to the first streamed token and to the end of the answer. */
  firstTokenMs?: number;
  durationMs?: number;
};

export type ChatSession = {
  id: string;
  title: string;
  modelId: string;
  modelName: string;
  createdAt: string;
  updatedAt: string;
  messages: ChatMessage[];
};

export type ChatModelOption = {
  id: string;
  name: string;
  group: string;
  capabilities: string[];
  /** Model ID sent to /v1/chat/completions when different from `id`. */
  requestModel?: string;
};

/** Optional request parameters; empty values are left out of the request. */
export type ChatParams = {
  systemPrompt: string;
  temperature: string;
  maxTokens: string;
};
