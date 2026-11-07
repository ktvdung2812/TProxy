import { isDeepStrictEqual } from 'node:util';

// Anthropic-shaped fixtures and checks shared by the fake upstream and smoke test.
const tools = [
  { type: 'tool_use', id: 'toolu_a', name: 'lookup', input: { query: 'alpha' } },
  { type: 'tool_use', id: 'toolu_b', name: 'second', input: { query: 'beta' } },
];

export function anthropic(req, res, body) {
  const last = body.messages?.filter(m => m.role === 'user').at(-1);
  const scenario = JSON.stringify(last?.content).match(/scenario:(tools2|maxtokens|stopseq|thinking)/)?.[1];
  const secret = (req.headers.authorization || '').replace(/^Bearer /i, '') || req.headers['x-api-key'] || '';
  const text = { type: 'text', text: `hello from ${secret}` };
  const content = scenario === 'tools2' ? [text, ...tools] : scenario === 'thinking'
    ? [{ type: 'thinking', thinking: 'Let me think.', signature: 'sig-abc' }, { type: 'redacted_thinking', data: 'redacted-xyz' }, text] : [text];
  const stop_reason = { tools2: 'tool_use', maxtokens: 'max_tokens', stopseq: 'stop_sequence' }[scenario] || 'end_turn';
  const stop_sequence = scenario === 'stopseq' ? 'END' : null;
  const message = { id: `msg-${secret}`, type: 'message', role: 'assistant', model: body.model, content, stop_reason, stop_sequence, usage: { input_tokens: 3, output_tokens: 4 } };
  if (!body.stream) { res.setHeader('Content-Type', 'application/json'); return res.end(JSON.stringify(message)); }
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const emit = event => res.write(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
  emit({ type: 'message_start', message: { ...message, content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 3, output_tokens: 0 } } });
  const start = (index, content_block) => emit({ type: 'content_block_start', index, content_block });
  const delta = (index, delta) => emit({ type: 'content_block_delta', index, delta });
  const stop = index => emit({ type: 'content_block_stop', index });
  if (scenario === 'tools2') {
    start(0, { type: 'text', text: '' }); delta(0, { type: 'text_delta', text: text.text }); stop(0);
    start(1, { ...tools[0], input: {} }); start(2, { ...tools[1], input: {} });
    delta(1, { type: 'input_json_delta', partial_json: '{"query":' });
    delta(2, { type: 'input_json_delta', partial_json: '{"query":' });
    delta(1, { type: 'input_json_delta', partial_json: '"alpha"}' });
    delta(2, { type: 'input_json_delta', partial_json: '"beta"}' }); stop(1); stop(2);
  } else {
    content.forEach((block, index) => {
      if (block.type === 'thinking') {
        start(index, { type: 'thinking', thinking: '' });
        delta(index, { type: 'thinking_delta', thinking: block.thinking });
        delta(index, { type: 'signature_delta', signature: block.signature });
      } else if (block.type === 'text') { start(index, { type: 'text', text: '' }); delta(index, { type: 'text_delta', text: block.text }); }
      else start(index, block);
      stop(index);
    });
  }
  emit({ type: 'message_delta', delta: { stop_reason, stop_sequence }, usage: { output_tokens: 4 } });
  emit({ type: 'message_stop' }); res.end();
}

function parseStream(body) {
  const message = { content: [] }; const json = {}; let stopped = false;
  for (const frame of body.split(/\r?\n\r?\n/)) {
    const data = frame.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
    if (!data) continue;
    const event = JSON.parse(data);
    if (event.type === 'message_start') Object.assign(message, event.message, { content: [] });
    if (event.type === 'content_block_start') message.content[event.index] = { ...event.content_block };
    if (event.type === 'content_block_delta') {
      const block = message.content[event.index];
      if (!block) throw new Error(`delta for missing block ${event.index}`);
      const d = event.delta;
      if (d.type === 'input_json_delta') json[event.index] = (json[event.index] || '') + d.partial_json;
      if (d.type === 'text_delta') block.text = (block.text || '') + d.text;
      if (d.type === 'thinking_delta') block.thinking = (block.thinking || '') + d.thinking;
      if (d.type === 'signature_delta') block.signature = (block.signature || '') + d.signature;
    }
    if (event.type === 'message_delta') Object.assign(message, event.delta);
    if (event.type === 'message_stop') stopped = true;
  }
  if (!stopped) throw new Error('missing message_stop');
  for (const [index, value] of Object.entries(json)) message.content[index].input = JSON.parse(value);
  return message;
}

export async function fetchWithRateLimitRetry(request) {
  let response = await request();
  if (response.status === 429) {
    await response.text();
    await new Promise(resolve => setTimeout(resolve, 65000));
    response = await request();
  }
  return response;
}

export async function fidelity(scenario, stream, base, key) {
  const request = () => fetch(`${base}/v1/messages`, { method: 'POST', headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json', 'anthropic-version': '2023-06-01' }, body: JSON.stringify({ model: 'dev-claude', max_tokens: 128, stream, messages: [{ role: 'user', content: `scenario:${scenario}` }] }), signal: AbortSignal.timeout(30000) });
  const response = await fetchWithRateLimitRetry(request);
  const body = await response.text();
  if (!response.ok) throw new Error(`HTTP ${response.status}: ${body.slice(0, 400)}`);
  if (stream && !response.headers.get('content-type')?.includes('text/event-stream')) throw new Error('expected text/event-stream');
  const message = stream ? parseStream(body) : JSON.parse(body);
  const blocks = message.content || [];
  const actual = { stop_reason: message.stop_reason, stop_sequence: message.stop_sequence ?? null, bearer: blocks.some(b => b?.text?.includes('hub-secret')) };
  const expected = { stop_reason: { tools2: 'tool_use', maxtokens: 'max_tokens', stopseq: 'stop_sequence', thinking: 'end_turn' }[scenario], stop_sequence: scenario === 'stopseq' ? 'END' : null, bearer: true };
  if (scenario === 'tools2') {
    actual.tools = blocks.filter(b => b?.type === 'tool_use').map(({ id, name, input }) => ({ type: 'tool_use', id, name, input }));
    expected.tools = tools;
  }
  if (scenario === 'thinking') {
    actual.thinking = blocks.filter(b => b?.type === 'thinking' || b?.type === 'redacted_thinking');
    expected.thinking = [{ type: 'thinking', thinking: 'Let me think.', signature: 'sig-abc' }, { type: 'redacted_thinking', data: 'redacted-xyz' }];
  }
  if (!isDeepStrictEqual(actual, expected)) throw new Error(`expected ${JSON.stringify(expected)}; got ${JSON.stringify(actual)}`);
}
