// Incremental SSE parser: push() raw decoded text, returns parsed data payloads.
// Pure JS (no vscode module) so it can be exercised by selfcheck.js.
class SSEDecoder {
  constructor() { this.buf = ''; this.events = []; }
  push(text) {
    this.buf += text;
    let idx;
    while ((idx = this.buf.indexOf('\n')) >= 0) {
      const line = this.buf.slice(0, idx).trim();
      this.buf = this.buf.slice(idx + 1);
      if (line.startsWith('data:')) this.events.push(line.slice(5).trim());
    }
    return this.events.splice(0);
  }
  end() {
    const rest = this.buf.trim();
    this.buf = '';
    return rest.startsWith('data:') ? [rest.slice(5).trim()] : [];
  }
}

// Accumulates OpenAI tool_call deltas (index -> {id,name,args}) into complete calls.
class ToolCallAccumulator {
  constructor() { this.slots = new Map(); }
  add(calls) {
    for (const tc of calls || []) {
      const slot = this.slots.get(tc.index) || { id: '', name: '', args: '' };
      if (tc.id) slot.id = tc.id;
      if (tc.function && tc.function.name) slot.name += tc.function.name;
      if (tc.function && tc.function.arguments) slot.args += tc.function.arguments;
      this.slots.set(tc.index, slot);
    }
  }
  complete() {
    return [...this.slots.entries()].sort((a, b) => a[0] - b[0]).map(([i, s], n) => {
      let input = {};
      try { input = JSON.parse(s.args || '{}'); } catch { /* malformed args */ }
      return { callId: s.id || `call_${n}`, name: s.name, input };
    });
  }
}

module.exports = { SSEDecoder, ToolCallAccumulator };
