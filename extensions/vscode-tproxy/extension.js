const vscode = require('vscode');
const { SSEDecoder, ToolCallAccumulator } = require('./sse');

const VENDOR = 'tproxy';
const SECRET_KEY = 'tproxy.apiKey';
const DEFAULT_BASE = 'http://127.0.0.1:28120';

function baseUrl() {
  const cfg = vscode.workspace.getConfiguration('tproxy').get('baseUrl');
  return (typeof cfg === 'string' && cfg.trim() ? cfg.trim() : DEFAULT_BASE).replace(/\/+$/, '');
}

function partText(content) {
  return (content || []).map((p) =>
    p instanceof vscode.LanguageModelTextPart ? p.value
      : typeof p === 'string' ? p
        : JSON.stringify(p)
  ).join('');
}

function convertMessages(messages) {
  const out = [];
  const roles = vscode.LanguageModelChatMessageRole;
  for (const msg of messages) {
    const role = msg.role === roles.Assistant ? 'assistant'
      : roles.System !== undefined && msg.role === roles.System ? 'system'
        : 'user';
    const texts = [];
    const images = [];
    const toolCalls = [];
    for (const part of msg.content) {
      if (part instanceof vscode.LanguageModelTextPart) {
        texts.push(part.value);
      } else if (part instanceof vscode.LanguageModelToolCallPart) {
        toolCalls.push(part);
      } else if (part instanceof vscode.LanguageModelToolResultPart) {
        out.push({ role: 'tool', tool_call_id: part.callId, content: partText(part.content) });
      } else if (vscode.LanguageModelDataPart && part instanceof vscode.LanguageModelDataPart && part.mimeType && part.mimeType.startsWith('image/')) {
        images.push({ type: 'image_url', image_url: { url: `data:${part.mimeType};base64,${Buffer.from(part.data).toString('base64')}` } });
      }
    }
    if (toolCalls.length) {
      out.push({
        role: 'assistant',
        content: texts.join('') || null,
        tool_calls: toolCalls.map((tc) => ({
          id: tc.callId,
          type: 'function',
          function: { name: tc.name, arguments: JSON.stringify(tc.input != null ? tc.input : {}) },
        })),
      });
    } else if (images.length) {
      out.push({ role, content: [...texts.map((t) => ({ type: 'text', text: t })), ...images] });
    } else if (texts.length) {
      out.push({ role, content: texts.join('') });
    }
  }
  return out;
}

function num(v) { return typeof v === 'number' && v > 0 ? Math.floor(v) : 0; }

class TProxyChatProvider {
  constructor(context) { this.context = context; }

  async request(path, init, token) {
    const key = await this.context.secrets.get(SECRET_KEY);
    const ac = new AbortController();
    const sub = token && token.onCancellationRequested(() => ac.abort());
    try {
      return await fetch(baseUrl() + path, {
        ...(init || {}),
        signal: ac.signal,
        headers: {
          ...(key ? { Authorization: `Bearer ${key}` } : {}),
          ...((init && init.headers) || {}),
        },
      });
    } finally {
      if (sub) sub.dispose();
    }
  }

  async provideLanguageModelChatInformation(options, token) {
    let res = await this.request('/v1/models', {}, token);
    if (res.status === 401) {
      if (options.silent) return [];
      if (!(await manage(this.context.secrets))) return [];
      res = await this.request('/v1/models', {}, token);
    }
    if (!res.ok) throw new Error(`tproxy /v1/models: HTTP ${res.status} ${await res.text()}`);
    const body = await res.json();
    return (body.data || []).filter((m) => m && m.id).map((m) => {
      const caps = Array.isArray(m.capabilities) ? m.capabilities : [];
      const limits = m.limits || {};
      return {
        id: m.id,
        name: m.name || m.id,
        family: m.name || m.id,
        version: '1.0.0',
        maxInputTokens: num(limits.context_window) || num(limits.max_input_tokens) || 128000,
        maxOutputTokens: num(limits.max_output_tokens) || 16384,
        detail: m.endpoint,
        tooltip: `${m.id} via tproxy`,
        capabilities: {
          imageInput: caps.includes('vision'),
          toolCalling: caps.includes('tools'),
        },
      };
    });
  }

  async provideLanguageModelChatResponse(model, messages, options, progress, token) {
    const key = await this.context.secrets.get(SECRET_KEY);
    const ac = new AbortController();
    const sub = token.onCancellationRequested(() => ac.abort());
    try {
      const body = {
        model: model.id,
        messages: convertMessages(messages),
        stream: true,
        stream_options: { include_usage: true },
      };
      if (options.tools && options.tools.length) {
        body.tools = options.tools.map((t) => ({
          type: 'function',
          function: {
            name: t.name,
            description: t.description || '',
            parameters: t.inputSchema || { type: 'object', properties: {} },
          },
        }));
        if (options.toolMode === vscode.LanguageModelChatToolMode.Required) body.tool_choice = 'required';
      }
      const res = await fetch(baseUrl() + '/v1/chat/completions', {
        method: 'POST',
        signal: ac.signal,
        headers: {
          'Content-Type': 'application/json',
          ...(key ? { Authorization: `Bearer ${key}` } : {}),
        },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        const text = await res.text();
        let msg = text;
        try { msg = JSON.parse(text).error.message; } catch { /* not json error */ }
        throw new Error(`tproxy HTTP ${res.status}: ${msg}`);
      }

      const sse = new SSEDecoder();
      const tools = new ToolCallAccumulator();
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      const handle = (payload) => {
        if (payload === '[DONE]' || !payload) return;
        const chunk = JSON.parse(payload);
        if (chunk.error) throw new Error(chunk.error.message || JSON.stringify(chunk.error));
        for (const choice of chunk.choices || []) {
          const delta = choice.delta || {};
          if (delta.content) progress.report(new vscode.LanguageModelTextPart(delta.content));
          if (delta.tool_calls) tools.add(delta.tool_calls);
        }
      };
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        for (const payload of sse.push(dec.decode(value, { stream: true }))) handle(payload);
      }
      for (const payload of sse.end()) handle(payload);
      for (const call of tools.complete()) {
        progress.report(new vscode.LanguageModelToolCallPart(call.callId, call.name, call.input));
      }
    } finally {
      sub.dispose();
    }
  }

  async provideTokenCount(model, text) {
    const str = typeof text === 'string' ? text : JSON.stringify(text.content);
    return Math.max(1, Math.ceil(str.length / 4));
  }
}

async function manage(secrets) {
  const url = await vscode.window.showInputBox({
    title: 'TProxy Base URL',
    prompt: 'Base URL of the tproxy gateway',
    value: baseUrl(),
    ignoreFocusOut: true,
  });
  if (url === undefined) return false;
  await vscode.workspace.getConfiguration('tproxy').update('baseUrl', url.trim() || DEFAULT_BASE, vscode.ConfigurationTarget.Global);
  const key = await vscode.window.showInputBox({
    title: 'TProxy API Key',
    prompt: 'Client API key. Leave empty if tproxy allows loopback requests without a key.',
    password: true,
    ignoreFocusOut: true,
  });
  if (key === undefined) return false;
  if (key.trim()) await secrets.store(SECRET_KEY, key.trim());
  else await secrets.delete(SECRET_KEY);
  return true;
}

function activate(context) {
  const provider = new TProxyChatProvider(context);
  context.subscriptions.push(
    vscode.lm.registerLanguageModelChatProvider(VENDOR, provider),
    vscode.commands.registerCommand('tproxy.manage', async () => {
      if (await manage(context.secrets)) {
        vscode.window.showInformationMessage('TProxy settings saved.');
      }
    })
  );
}

function deactivate() {}

module.exports = { activate, deactivate };
