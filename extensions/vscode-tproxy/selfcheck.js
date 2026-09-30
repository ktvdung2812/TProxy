const assert = require('assert');
const { SSEDecoder, ToolCallAccumulator } = require('./sse.js');

// SSE splits events across chunk boundaries
const sse = new SSEDecoder();
assert.deepStrictEqual(sse.push('data: {"a":1}\nda'), ['{"a":1}']);
assert.deepStrictEqual(sse.push('ta: {"b":2}\n\ndata: [DONE]\n'), ['{"b":2}', '[DONE]']);
assert.deepStrictEqual(sse.push(''), []);
assert.deepStrictEqual(sse.end(), []);

// trailing event without newline is flushed by end()
const sse2 = new SSEDecoder();
sse2.push('data: {"c":3}');
assert.deepStrictEqual(sse2.end(), ['{"c":3}']);

// tool call deltas accumulate into complete calls in index order
const acc = new ToolCallAccumulator();
acc.add([{ index: 0, id: 'call_1', function: { name: 'read_', arguments: '{"pa' } }]);
acc.add([{ index: 1, id: 'call_2', function: { name: 'write', arguments: '{"x":' } }]);
acc.add([{ index: 0, function: { name: 'file', arguments: 'th":"/tmp"}' } }]);
acc.add([{ index: 1, function: { arguments: '2}' } }]);
assert.deepStrictEqual(acc.complete(), [
  { callId: 'call_1', name: 'read_file', input: { path: '/tmp' } },
  { callId: 'call_2', name: 'write', input: { x: 2 } },
]);

// malformed args fall back to {} instead of throwing
const bad = new ToolCallAccumulator();
bad.add([{ index: 0, id: 'call_3', function: { name: 'f', arguments: 'not-json' } }]);
assert.deepStrictEqual(bad.complete(), [{ callId: 'call_3', name: 'f', input: {} }]);

console.log('selfcheck ok');
