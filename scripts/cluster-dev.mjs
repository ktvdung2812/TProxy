#!/usr/bin/env node
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { findGo } from './toolchain.mjs';
import { anthropic, fidelity, fetchWithRateLimitRetry } from './cluster-dev/fidelity.mjs';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const dir = join(root, '.dev/cluster');
const binary = join(dir, 'bin/tproxy');
const nodes = { hub: 28171, 'member-a': 28172, 'member-b': 28173 };
const ports = { ...nodes, upstream: 28179, web: 28175 };
const url = n => `http://127.0.0.1:${ports[n]}`;
const file = (n, f) => join(dir, n, f);
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const read = p => JSON.parse(readFileSync(p, 'utf8'));
const write = (p, data) => writeFileSync(p, JSON.stringify(data, null, 2) + '\n', { mode: 0o600 });
const env = n => read(file(n, 'env.json'));
function run(cmd, args, options = {}) {
  const result = spawnSync(cmd, args, { encoding: 'utf8', ...options });
  if (result.error || result.status !== 0) throw new Error(`${cmd}: ${result.error?.message || result.stderr || result.stdout || `exit ${result.status}`}`);
  return result.stdout.trim();
}
function identity(pid) {
  const result = spawnSync('ps', ['-p', String(pid), '-o', 'lstart=', '-o', 'command='], { encoding: 'utf8' });
  return result.status === 0 ? result.stdout.trim() : '';
}
function running(n) {
  const p = file(n, 'pid.json');
  if (!existsSync(p)) return false;
  const record = read(p);
  return !!record.identity && identity(record.pid) === record.identity;
}
async function freePort(port) {
  await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(port, '127.0.0.1', () => server.close(resolve));
  }).catch(e => { throw new Error(`port ${port} is unavailable: ${e.message}`); });
}
async function start(n, command, args, extra = {}) {
  if (running(n)) { console.log(`${n}: already running`); return; }
  await freePort(ports[n]);
  mkdirSync(join(dir, n), { recursive: true, mode: 0o700 });
  const fd = openSync(file(n, 'log'), 'a', 0o600);
  const child = spawn(command, args, { cwd: extra.cwd || join(dir, n), env: { ...process.env, HOME: join(dir, n), ...extra.env }, detached: true, stdio: ['ignore', fd, fd] });
  closeSync(fd);
  child.unref();
  await sleep(150);
  const id = identity(child.pid);
  if (!id) throw new Error(`${n} failed to start; see ${file(n, 'log')}`);
  write(file(n, 'pid.json'), { pid: child.pid, identity: id });
  console.log(`${n}: started on ${ports[n]} (pid ${child.pid})`);
}
async function waitFor(label, fn, timeout = 30000) {
  const end = Date.now() + timeout;
  let last;
  while (Date.now() < end) {
    try { const value = await fn(); if (value) return value; } catch (e) { last = e; }
    await sleep(300);
  }
  throw new Error(`timed out waiting for ${label}${last ? `: ${last.message}` : ''}`);
}
async function api(n, path, method = 'GET', body, key = env(n).managementSecret) {
  const response = await fetch(url(n) + path, { method, headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(30000) });
  const text = await response.text();
  if (!response.ok) throw new Error(`${n} ${method} ${path}: HTTP ${response.status} ${text}`);
  try { return JSON.parse(text); } catch { return text; }
}
const cluster = n => api(n, '/api/admin/cluster');
function cli(n, args) { return run(binary, ['cluster', ...args, '--url', url(n), '--secret', env(n).managementSecret], { cwd: join(dir, n), env: { ...process.env, HOME: join(dir, n) } }); }
function config(n) {
  const provider = { hub: 'hub', 'member-a': 'a', 'member-b': 'b' }[n];
  const models = n === 'hub' ? `\n  - id: dev-chat\n    display-name: dev-chat\n    enabled: true\n    routes:\n      - id: hub-chat\n        provider: up-hub\n        upstream-model: up-model\n        priority: 10\n        weight: 1\n  - id: dev-fast\n    display-name: dev-fast\n    enabled: true\n    routes:\n      - id: hub-fast\n        provider: up-hub\n        upstream-model: up-model\n        priority: 10\n        weight: 1\n  - id: a-only\n    display-name: a-only\n    enabled: true\n    routes: []` : ' []';
  return `server:\n  host: 127.0.0.1\n  port: ${ports[n]}\ndatabase:\n  driver: sqlite\n  dsn: tproxy.db\nsecurity:\n  master-key-env: TPROXY_MASTER_KEY\n  management-secret-env: TPROXY_MANAGEMENT_SECRET\nproviders:\n  - id: up-${provider}\n    type: openai-compatible\n    name: upstream-${provider}\n    base-url: ${url('upstream')}\n    enabled: true\n    credentials:\n      - id: ${provider}-credential\n        auth-type: api_key\n        secret-env: DEV_UPSTREAM_SECRET\nmodels:${models}\n`;
}
async function joinMember(n) {
  const token = cli('hub', ['token', '--label', n]).match(/Token: (tpc1_\S+)/)?.[1];
  if (!token) throw new Error('token command did not return a token');
  cli(n, ['join', token, '--name', n, '--allow-insecure']);
}
async function bootstrap() {
  if ((await cluster('hub')).role === 'standalone') {
    cli('hub', ['create', '--name', 'dev', '--hub-url', url('hub'), '--allow-insecure']);
    console.log('Created cluster dev');
  }
  const hub = await cluster('hub');
  if (hub.role !== 'hub' || hub.cluster?.name !== 'dev') throw new Error('hub is in an unexpected cluster');
  const snapshotHub = await api('hub', '/api/admin/snapshot');
  if (!snapshotHub.providers?.some(p => (p.ID || p.id) === 'up-claude')) {
    await api('hub', '/api/admin/providers', 'POST', { id: 'up-claude', type: 'anthropic-compatible', name: 'upstream-claude', base_url: url('upstream'), enabled: true, credentials: [{ id: 'claude-credential', auth_type: 'api_key', secret: 'hub-secret' }] });
    console.log('hub: added Anthropic provider');
  }
  if (!snapshotHub.models?.some(m => (m.ID || m.id) === 'dev-claude')) {
    await api('hub', '/api/admin/models', 'POST', { id: 'dev-claude', display_name: 'dev-claude', enabled: true, routes: [{ id: 'hub-claude', provider: 'up-claude', upstream_model: 'up-model', priority: 10, weight: 1 }] });
    console.log('hub: added dev-claude');
  }
  const shared = hub.shared || {};
  const models = [...new Set([...(shared.models || []), 'dev-chat', 'dev-fast', 'a-only', 'dev-claude'])];
  if (models.length !== (shared.models || []).length) await api('hub', '/api/admin/cluster/shared', 'PUT', { models, combos: shared.combos || [], api_keys: shared.api_keys || [] });
  for (const n of ['member-a', 'member-b']) {
    const status = await cluster(n);
    if (status.role === 'standalone') { await joinMember(n); console.log(`${n}: joined`); }
    else if (status.role !== 'member' || status.cluster?.hub_url !== url('hub')) throw new Error(`${n} belongs to an unexpected cluster`);
  }
  await waitFor('member-a catalog', async () => (await cluster('member-a')).catalog?.models?.some(m => m.id === 'a-only'));
  const snapshot = await api('member-a', '/api/admin/snapshot');
  const model = snapshot.models?.find(m => (m.id || m.ID) === 'a-only');
  if (!model) throw new Error('a-only missing from member-a snapshot');
  // Snapshot uses exported Go field names; use the managed catalog's public fields.
  const catalogModel = (await cluster('member-a')).catalog.models.find(m => m.id === 'a-only');
  if (!(snapshot.routes?.['a-only'] || []).some(r => r.ProviderID === 'up-a')) {
    await api('member-a', '/api/admin/models', 'POST', { id: 'a-only', display_name: model.DisplayName ?? model.display_name ?? catalogModel.display_name, aliases: model.Aliases ?? model.aliases, enabled: model.Enabled ?? model.enabled, expose_upstream_name: model.ExposeUpstreamName ?? model.expose_upstream_name, rewrite_response_model: model.RewriteResponseModel ?? model.rewrite_response_model, capabilities: model.Capabilities ?? model.capabilities, limits: model.Limits ?? model.limits, routes: [{ id: 'a-local', provider: 'up-a', upstream_model: 'up-model', priority: 10, weight: 1 }] });
    console.log('member-a: attached a-only route');
  }
  await waitFor('member-b Claude catalog', async () => (await cluster('member-b')).catalog?.models?.some(m => m.id === 'dev-claude'));
  for (const n of Object.keys(nodes)) {
    const e = env(n);
    if (!e.clientKey) {
      const key = await api(n, '/api/admin/api-keys', 'POST', { name: `${n} dev client` });
      e.clientKey = key.key; e.clientKeyID = key.id; write(file(n, 'env.json'), e);
      console.log(`${n}: created client key`);
    }
  }
  await waitFor('connected members and offers', async () => {
    const h = await cluster('hub');
    const members = await Promise.all(['member-a', 'member-b'].map(cluster));
    return members.every(m => m.link?.state === 'connected') && members.every(m => h.nodes?.some(x => x.id === m.node.id && x.online)) && h.nodes.some(x => x.id === members[0].node.id && x.offers?.some(o => o.model_id === 'a-only' && o.eligible_accounts > 0));
  }, 45000);
}
async function up(web) {
  mkdirSync(join(dir, 'bin'), { recursive: true, mode: 0o700 });
  const go = findGo(); if (!go) throw new Error('Go 1.26 or newer is required');
  console.log('Building tproxy...');
  run(go, ['build', '-o', binary, './cmd/tproxy'], { cwd: root });
  for (const n of Object.keys(nodes)) {
    mkdirSync(join(dir, n), { recursive: true, mode: 0o700 });
    if (!existsSync(file(n, 'config.yaml'))) writeFileSync(file(n, 'config.yaml'), config(n), { mode: 0o600 });
    if (!existsSync(file(n, 'env.json'))) write(file(n, 'env.json'), { masterKey: run(binary, ['--print-master-key'], { cwd: join(dir, n), env: { ...process.env, HOME: join(dir, n) } }), managementSecret: randomBytes(32).toString('hex') });
  }
  await start('upstream', process.execPath, [fileURLToPath(import.meta.url), '_upstream']);
  await waitFor('upstream', async () => (await fetch(url('upstream') + '/v1/models')).ok);
  for (const n of Object.keys(nodes)) {
    const e = env(n);
    await start(n, binary, ['--config', file(n, 'config.yaml')], { env: { TPROXY_MASTER_KEY: e.masterKey, TPROXY_MANAGEMENT_SECRET: e.managementSecret, TPROXY_SKIP_TUNNEL_AUTO: '1', DEV_UPSTREAM_SECRET: { hub: 'hub-secret', 'member-a': 'a-secret', 'member-b': 'b-secret' }[n] } });
    await waitFor(`${n} health`, async () => (await fetch(url(n) + '/healthz')).ok);
  }
  await bootstrap();
  if (web) {
    const vite = join(root, 'web/node_modules/vite/bin/vite.js');
    if (!existsSync(vite)) throw new Error('web dependencies missing; run npm install in web first');
    await start('web', process.execPath, [vite, '--host', '127.0.0.1', '--port', '28175', '--strictPort'], { cwd: join(root, 'web'), env: { TPROXY_DEV_PORT: '28175', TPROXY_DEV_BACKEND: url('hub') } });
    await waitFor('dashboard', async () => (await fetch(url('web') + '/dashboard/')).ok);
    console.log(`Dashboard: ${url('web')}/dashboard/\nManagement secret: ${env('hub').managementSecret}`);
  }
  console.log('Cluster ready');
}
async function down() {
  for (const n of ['web', 'member-b', 'member-a', 'hub', 'upstream']) {
    const p = file(n, 'pid.json');
    if (!existsSync(p)) continue;
    if (running(n)) {
      const record = read(p); process.kill(record.pid, 'SIGTERM');
      await waitFor(`${n} shutdown`, () => !running(n), 10000);
      console.log(`${n}: stopped`);
    } else console.log(`${n}: not running (stale PID file)`);
    rmSync(p, { force: true });
  }
}
async function smoke(skipFidelity) {
  let failures = 0;
  async function check(name, fn) { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failures++; console.log(`FAIL ${name}: ${e.message}`); } }
  await check('nodes online and member links connected', async () => {
    await waitFor('online links', async () => { const h = await cluster('hub'); const m = await Promise.all(['member-a', 'member-b'].map(cluster)); return h.role === 'hub' && m.every(x => x.link?.state === 'connected' && h.nodes.some(y => y.id === x.node.id && y.online && y.status === 'active')); }, 20000);
  });
  async function chat(n, model, stream, secret) {
    const response = await fetchWithRateLimitRetry(() => fetch(url(n) + '/v1/chat/completions', { method: 'POST', headers: { Authorization: `Bearer ${env(n).clientKey}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ model, stream, messages: [{ role: 'user', content: 'hi' }] }), signal: AbortSignal.timeout(30000) }));
    const body = await response.text();
    if (!response.ok || !body.includes(`hello from ${secret}`) || (stream && (!response.headers.get('content-type')?.includes('text/event-stream') || !body.includes('[DONE]')))) throw new Error(`HTTP ${response.status}: ${body}`);
  }
  await check('member-b dev-chat (non-stream, hub-secret)', () => chat('member-b', 'dev-chat', false, 'hub-secret'));
  await check('member-b dev-chat (stream, hub-secret)', () => chat('member-b', 'dev-chat', true, 'hub-secret'));
  await check('hub a-only (a-secret)', () => chat('hub', 'a-only', false, 'a-secret'));
  await check('member-b a-only relay (a-secret)', () => chat('member-b', 'a-only', false, 'a-secret'));
  await check('hub ledger has rows', () => waitFor('ledger rows', async () => (await api('hub', '/api/admin/cluster/ledger?days=7')).rows?.some(r => r.requests > 0), 22000));
  await check('member-b revoke and re-join', async () => {
    const id = (await cluster('member-b')).node.id;
    await api('hub', `/api/admin/cluster/nodes/${encodeURIComponent(id)}`, 'DELETE');
    try {
      await waitFor('revoked link', async () => (await cluster('member-b')).link?.state === 'revoked', 20000);
    } finally {
      await joinMember('member-b');
    }
    await waitFor('member-b reconnected', async () => { const b = await cluster('member-b'); const h = await cluster('hub'); return b.link?.state === 'connected' && h.nodes.some(n => n.id === b.node.id && n.online && n.status === 'active'); }, 30000);
    await chat('member-b', 'dev-chat', false, 'hub-secret');
  });
  if (!skipFidelity) for (const scenario of ['tools2', 'maxtokens', 'stopseq', 'thinking']) for (const stream of [false, true]) for (const n of ['hub', 'member-b']) {
    await check(`fidelity ${scenario} ${stream ? 'stream' : 'non-stream'} ${n === 'hub' ? 'direct' : 'relayed'}`, () => fidelity(scenario, stream, url(n), env(n).clientKey));
  }
  if (failures) process.exitCode = 1;
}
function upstream() {
  http.createServer(async (req, res) => {
    if (req.method === 'GET' && req.url === '/v1/models') { res.setHeader('Content-Type', 'application/json'); return res.end(JSON.stringify({ object: 'list', data: [{ id: 'up-model', object: 'model' }] })); }
    if (req.method !== 'POST' || !['/v1/chat/completions', '/v1/messages'].includes(req.url)) { res.writeHead(404); return res.end(); }
    try {
      let data = ''; for await (const chunk of req) data += chunk;
      const body = JSON.parse(data);
      if (req.url === '/v1/messages') return anthropic(req, res, body);
      const content = `hello from ${(req.headers.authorization || '').replace(/^Bearer /, '')}`;
      const usage = { prompt_tokens: 3, completion_tokens: 4, total_tokens: 7 };
      if (!body.stream) { res.setHeader('Content-Type', 'application/json'); return res.end(JSON.stringify({ id: 'chatcmpl-dev', object: 'chat.completion', model: body.model, choices: [{ index: 0, message: { role: 'assistant', content }, finish_reason: 'stop' }], usage })); }
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      for (const chunk of [{ id: 'chatcmpl-dev', object: 'chat.completion.chunk', model: body.model, choices: [{ index: 0, delta: { role: 'assistant', content }, finish_reason: null }] }, { id: 'chatcmpl-dev', object: 'chat.completion.chunk', model: body.model, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] }, { id: 'chatcmpl-dev', object: 'chat.completion.chunk', model: body.model, choices: [], usage }]) res.write(`data: ${JSON.stringify(chunk)}\n\n`);
      res.end('data: [DONE]\n\n');
    } catch (e) { res.writeHead(400); res.end(e.message); }
  }).listen(28179, '127.0.0.1', () => console.log('fake upstream listening on 127.0.0.1:28179'));
}
try {
  const [command, ...args] = process.argv.slice(2);
  if (command === '_upstream') upstream();
  else if (command === 'up' && args.every(x => x === '--web')) await up(args.includes('--web'));
  else if (command === 'down' && !args.length) await down();
  else if (command === 'reset' && !args.length) { await down(); rmSync(dir, { recursive: true, force: true }); console.log('Cluster data removed'); }
  else if (command === 'status' && !args.length) for (const n of Object.keys(nodes)) console.log(`\n${n}:\n${cli(n, ['status'])}`);
  else if (command === 'smoke' && args.every(x => x === '--skip-fidelity')) await smoke(args.includes('--skip-fidelity'));
  else if (command === 'logs' && args.length <= 1 && (!args[0] || Object.hasOwn(ports, args[0]))) {
    const names = args.length ? args : Object.keys(ports).filter(n => existsSync(file(n, 'log')));
    if (!names.length) throw new Error('no logs yet');
    const result = spawnSync('tail', ['-n', '50', '-f', ...names.map(n => file(n, 'log'))], { stdio: 'inherit' });
    if (result.error) throw result.error;
  } else throw new Error('Usage: node scripts/cluster-dev.mjs up [--web] | down | reset | status | logs [hub|member-a|member-b|upstream|web] | smoke [--skip-fidelity]');
} catch (e) { console.error(`Error: ${e.message}`); process.exitCode = 1; }
