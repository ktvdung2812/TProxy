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
