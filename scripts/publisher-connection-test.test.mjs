import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { once } from 'node:events';
import { readFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

// Explicit local fixtures, never live credentials or a simulated production success.
const module = await import('./publisher-connection-test.mjs');
const token = 'fixture-publisher-key-'.repeat(3);
const challenge = 'fixture_random_challenge_0123456789';
const env = { GITHUB_REPOSITORY: 'Example/services', GITHUB_REF: 'refs/heads/main', GITHUB_RUN_ID: '12345' };
const options = { endpoint: 'https://fixture.invalid/api/images/published', token, connectionId: 'fixture_connection-123', challenge, env, baseDelayMs: 0 };
const send = settings => { assert.equal(typeof module.sendConnectionTest, 'function', 'sender export must exist'); return module.sendConnectionTest(settings); };
async function server(t, handler) {
  const instance = http.createServer(handler);
  instance.listen(0, '127.0.0.1');
  await once(instance, 'listening');
  t.after(() => { instance.closeAllConnections(); instance.close(); });
  return `https://127.0.0.1:${instance.address().port}/api/images/published`;
}
test('validates configuration without repairing it or exposing values', async () => {
  let requests = 0;
  const transport = async () => { requests++; return new Response('{"result":"verified"}'); };
  const invalid = {
    endpoint: [undefined, '', 'http://fixture.invalid/api/images/published', 'https://user:password@fixture.invalid/api/images/published', 'https://@fixture.invalid/api/images/published', 'https://fixture.invalid/wrong', 'https://fixture.invalid/api/images/published?', 'https://fixture.invalid/api/images/published#', 'https://fixture.invalid/api/images/published?token=private', 'https://fixture.invalid/api/other/../images/published', 'https://fixture.invalid/api/images/%70ublished', ' https://fixture.invalid/api/images/published', 'https://fixture.invalid\\\\api/images/published'],
    token: [undefined, '', 'x'.repeat(31), 'x'.repeat(4097), token + '\n', ' '.repeat(40)],
    connectionId: [undefined, '', 'x'.repeat(257), 'with space', 'padded=', '../invalid', 42],
    challenge: [undefined, '', 'x'.repeat(15), 'x'.repeat(513), challenge + '\n', 'invalid+base64/value=', 42],
  };
  for (const [key, values] of Object.entries(invalid)) for (const value of values) {
    await assert.rejects(send({ ...options, transport, [key]: value }), error => /^Invalid [a-z ]+$/.test(error.message), key);
  }
  for (const [key, value] of [['GITHUB_REPOSITORY', 'owner/repo/extra'], ['GITHUB_REPOSITORY', ''], ['GITHUB_REF', 'main'], ['GITHUB_REF', 'refs/heads/a..b'], ['GITHUB_REF', 'refs/heads/a.lock'], ['GITHUB_REF', 'refs/heads/main\n'], ['GITHUB_RUN_ID', '01'], ['GITHUB_RUN_ID', '']]) {
    await assert.rejects(send({ ...options, transport, env: { ...env, [key]: value } }), /Invalid provenance/);
  }
  assert.equal(requests, 0);
  for (const connectionId of ['a', '123e4567-e89b-12d3-a456-426614174000', 'random_base64url-ID']) {
    assert.equal(await send({ ...options, connectionId, transport }), 'verified');
  }
});

test('rejects redirects, non-200 status and unknown or oversized acknowledgements without retry', async t => {
  for (const [status, body] of [[302, '{}'], [307, '{}'], [401, token], [403, challenge], [409, '{}'], [201, '{"result":"verified"}'], [204, ''], [200, 'not JSON'], [200, '{"result":"unknown"}'], [200, '{"result":"accepted"}'], [200, '{"result":"verified","extra":true}'], [200, ' '.repeat(4097) + '{"result":"verified"}']]) {
    let requests = 0;
    const endpoint = await server(t, (req, res) => { requests++; res.writeHead(status, { Location: '/must-not-follow' }); res.end(body); });
    await assert.rejects(send({ ...options, endpoint, transport: localTransport }), /^Error: Connection test rejected or invalid acknowledgement$/);
    assert.equal(requests, 1);
  }
});

test('retries transient HTTP failures with identical serialized proof at most three times', async t => {
  for (const status of [408, 429, 500, 503, 599]) {
    const bodies = [];
    const endpoint = await server(t, async (req, res) => {
      let body = ''; for await (const chunk of req) body += chunk;
      bodies.push(body); res.statusCode = bodies.length === 1 ? status : 200; res.end('{"result":"duplicate"}');
    });
    assert.equal(await send({ ...options, endpoint, transport: localTransport }), 'duplicate');
    assert.equal(bodies.length, 2); assert.equal(bodies[0], bodies[1]);
  }
  let requests = 0;
  const endpoint = await server(t, (req, res) => { requests++; res.statusCode = 503; res.end(token); });
  await assert.rejects(send({ ...options, endpoint, transport: localTransport }), /Connection test failed after 3 attempts/);
  assert.equal(requests, 3);
});

test('retries real connection resets but not permanent transport errors, sanitizing exception details', async t => {
  let requests = 0;
  const endpoint = await server(t, (req, res) => { requests++; if (requests === 1) req.socket.destroy(); else res.end('{"result":"duplicate"}'); });
  assert.equal(await send({ ...options, endpoint, transport: localTransport }), 'duplicate');
  assert.equal(requests, 2);
  for (const code of ['ECONNRESET', 'EAI_AGAIN', 'ETIMEDOUT', 'CERT_HAS_EXPIRED', 'ENOTFOUND', undefined]) {
    let attempts = 0;
    await assert.rejects(send({ ...options, transport: async () => { attempts++; throw new Error(`${token} ${challenge}`, { cause: { code } }); } }), error => !error.message.includes(token) && !error.message.includes(challenge) && !error.cause);
    assert.equal(attempts, ['ECONNRESET', 'EAI_AGAIN', 'ETIMEDOUT'].includes(code) ? 3 : 1);
  }
});

test('bounds each attempt including stalled headers and response body to the configured timeout', { timeout: 2000 }, async t => {
  for (const headers of [false, true]) {
    const endpoint = await server(t, (req, res) => { if (headers) { res.writeHead(200); res.write('{'); } });
    await assert.rejects(send({ ...options, endpoint, transport: localTransport, timeoutMs: 40, maxAttempts: 2 }), /Connection test failed after 2 attempts/);
  }
  // A custom transport cannot bypass the deadline by ignoring AbortSignal.
  await assert.rejects(send({ ...options, transport: () => new Promise(() => {}), timeoutMs: 10, maxAttempts: 1 }), /Connection test failed after 1 attempts/);
});

test('refuses retry settings that exceed production bounds before sending', async () => {
  let requests = 0;
  const transport = async () => { requests++; return new Response('{"result":"verified"}'); };
  for (const bad of [{ maxAttempts: 4 }, { maxAttempts: 0 }, { maxAttempts: 1.5 }, { timeoutMs: 10001 }, { timeoutMs: 0 }, { timeoutMs: Infinity }, { baseDelayMs: -1 }, { baseDelayMs: 1001 }]) {
    await assert.rejects(send({ ...options, transport, ...bad }), /Invalid retry options/);
  }
  assert.equal(requests, 0);
});

test('CLI maps environment to the real sender and refuses credential arguments', async t => {
  assert.equal(typeof module.runCli, 'function');
  let received;
  const endpoint = await server(t, async (req, res) => { let body = ''; for await (const chunk of req) body += chunk; received = JSON.parse(body); res.end('{"result":"verified"}'); });
  const cliEnv = { ...env, SERVICES_PUBLISH_URL: endpoint, SERVICES_PUBLISH_TOKEN: token, SERVICES_CONNECTION_ID: options.connectionId, SERVICES_CONNECTION_CHALLENGE: challenge };
  assert.equal(await module.runCli([], cliEnv, { transport: localTransport }), 'verified');
  assert.equal(received.connectionId, options.connectionId);
  assert.equal(received.challenge, challenge);
  await assert.rejects(module.runCli([token], cliEnv), /Invalid arguments/);
  const execute = promisify(execFile);
  for (const args of [[], [token]]) {
    await assert.rejects(execute(process.execPath, ['scripts/publisher-connection-test.mjs', ...args], {
      env: { ...process.env, ...cliEnv, SERVICES_PUBLISH_URL: 'http://fixture.invalid/api/images/published' },
    }), error => error.code === 1 && error.stdout === '' && error.stderr.trim() === 'Services connection test failed' && !error.stderr.includes(token) && !error.stderr.includes(challenge));
  }
});

test('standalone dispatch workflow requires configuration and sends no image publication', async () => {
  const workflow = await readFile(new URL('../.github/workflows/services-connection-test.yml', import.meta.url), 'utf8').catch(() => '');
  assert.match(workflow, /^name: services-connection-test/m);
  assert.match(workflow, /^  workflow_dispatch:\n    inputs:/m);
  for (const input of ['connection_id', 'challenge']) {
    assert.match(workflow, new RegExp(`      ${input}:\\n        description: [^\\n]+\\n        required: true\\n        type: string`));
  }
  assert.match(workflow, /contents: read/); assert.match(workflow, /id-token: write/);
  assert.match(workflow, /runs-on: ubuntu-latest/); assert.match(workflow, /timeout-minutes: [1-9]\b/);
  assert.match(workflow, /uses: actions\/checkout@v6/); assert.match(workflow, /uses: actions\/setup-node@v7/);
  assert.match(workflow, /node-version: '24'/); assert.match(workflow, /uses: tailscale\/github-action@v4/);
  assert.match(workflow, /log-mode: quiet/);
  for (const [key, value] of [['oauth-client-id', 'TAILSCALE_CLIENT_ID'], ['audience', 'TAILSCALE_AUDIENCE'], ['tags', 'TAILSCALE_TAGS']]) assert.ok(workflow.includes(`${key}: \${{ vars.${value} }}`));
  for (const variable of ['TAILSCALE_CLIENT_ID', 'TAILSCALE_AUDIENCE', 'TAILSCALE_TAGS', 'SERVICES_PUBLISH_URL', 'SERVICES_PUBLISH_TOKEN']) assert.ok(workflow.includes(`'${variable}'`));
  for (const [key, expression] of [['SERVICES_PUBLISH_URL', 'vars.SERVICES_PUBLISH_URL'], ['SERVICES_PUBLISH_TOKEN', 'secrets.SERVICES_PUBLISH_TOKEN'], ['SERVICES_CONNECTION_ID', 'inputs.connection_id'], ['SERVICES_CONNECTION_CHALLENGE', 'inputs.challenge']]) assert.ok(workflow.includes(`${key}: \${{ ${expression} }}`));
  assert.ok(workflow.indexOf('Require configuration') < workflow.indexOf('uses: tailscale/github-action@v4'));
  assert.ok(workflow.indexOf('uses: tailscale/github-action@v4') < workflow.indexOf('run: node scripts/publisher-connection-test.mjs'));
  assert.doesNotMatch(workflow, /continue-on-error|docker|build-push|packages: write|oauth-secret|authkey|publish-notification/);
  for (const block of workflow.split(/\n      - name:/).slice(1)) {
    const run = block.match(/\n        run:([\s\S]*)/)?.[1] ?? '';
    assert.doesNotMatch(run, /\$\{\{/);
  }
});

const localTransport = (url, init) => { const local = new URL(url); local.protocol = 'http:'; return fetch(local, init); };

test('cross-origin redirects never forward the callback credential or challenge', async t => {
  let forwarded = 0;
  const destination = await server(t, (req, res) => { forwarded++; res.end('{"result":"verified"}'); });
  for (const status of [301, 302, 303, 307, 308]) {
    let requests = 0;
    const endpoint = await server(t, (req, res) => { requests++; res.writeHead(status, { Location: destination.replace('https:', 'http:') }); res.end(); });
    await assert.rejects(send({ ...options, endpoint, transport: localTransport }), /Connection test rejected/);
    assert.equal(requests, 1);
  }
  assert.equal(forwarded, 0);
});

test('retries freeze callback proof and provenance despite mutable environment', async () => {
  const mutableEnv = { ...env };
  const requests = [];
  const transport = async (url, init) => {
    requests.push({ url, body: init.body, headers: init.headers, redirect: init.redirect });
    mutableEnv.GITHUB_REPOSITORY = 'untrusted/changed';
    mutableEnv.GITHUB_REF = 'refs/heads/changed';
    mutableEnv.GITHUB_RUN_ID = '99999';
    return new Response(requests.length === 1 ? '' : '{"result":"duplicate"}', { status: requests.length === 1 ? 503 : 200 });
  };
  assert.equal(await send({ ...options, env: mutableEnv, transport }), 'duplicate');
  assert.equal(requests.length, 2);
  assert.deepEqual(requests[0], requests[1]);
  assert.deepEqual(Object.keys(requests[0].headers).sort(), ['Authorization', 'Content-Type']);
  assert.equal(requests[0].redirect, 'manual');
  assert.equal(JSON.parse(requests[0].body).sourceRepository, env.GITHUB_REPOSITORY);
});

test('standalone workflow exposes credentials only through environment and only supports manual dispatch', async () => {
  const workflow = await readFile(new URL('../.github/workflows/services-connection-test.yml', import.meta.url), 'utf8');
  const triggers = workflow.split(/^on:\s*$/m)[1]?.split(/^[^\s#]/m)[0] ?? '';
  assert.deepEqual([...triggers.matchAll(/^  ([a-z_]+):/gm)].map(match => match[1]), ['workflow_dispatch']);
  assert.match(workflow, /persist-credentials: false/);
  assert.doesNotMatch(workflow, /pull_request_target|workflow_run|repository_dispatch|secrets: inherit/);
  assert.doesNotMatch(workflow, /(?:run:|node scripts\/publisher-connection-test\.mjs).*\$\{\{\s*(?:secrets|inputs)\./);
});

test('posts only connection proof and GitHub provenance to the authenticated callback', async t => {
  for (const result of ['verified', 'duplicate']) {
    let received;
    const endpoint = await server(t, async (req, res) => {
      let body = ''; for await (const chunk of req) body += chunk;
      received = { path: req.url, method: req.method, auth: req.headers.authorization, type: req.headers['content-type'], body };
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ result }));
    });
    assert.equal(await send({ ...options, endpoint, transport: localTransport }), result);
    assert.deepEqual(received, { path: '/api/publishers/callback', method: 'POST', auth: `Bearer ${token}`, type: 'application/json', body: JSON.stringify({ connectionId: options.connectionId, challenge, sourceRepository: env.GITHUB_REPOSITORY, ref: env.GITHUB_REF, runId: env.GITHUB_RUN_ID }) });
  }
});
