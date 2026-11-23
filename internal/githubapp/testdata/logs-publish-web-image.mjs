import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { inspect } from './release.mjs';

const host = 'ghcr.io';
const digestPattern = /^sha256:[a-f0-9]{64}$/;
const imagePattern = /^ghcr\.io\/([a-z0-9_.-]+\/logs):(0\.1\.[1-9]\d{0,8})$/;
const manifestPattern = /^ghcr\.io\/([a-z0-9_.-]+\/logs):(0\.1\.[1-9]\d{0,8}|latest)$/;
const manifestTypes = ['application/vnd.oci.image.manifest.v1+json', 'application/vnd.docker.distribution.manifest.v2+json'];
const timeout = () => AbortSignal.timeout(30_000);
function requireThat(value, message) { if (!value) throw new Error(message); }

async function fetchManifest(image, token, request) {
  const match = manifestPattern.exec(image);
  requireThat(match, 'Invalid Web registry reference');
  const [, name, tag] = match;
  const response = await request(`https://${host}/v2/${name}/manifests/${tag}`, {
    headers: { Authorization: `Bearer ${token}`, Accept: manifestTypes.join(', ') },
    redirect: 'error', signal: timeout(),
  });
  if (response.status === 404) {
    const { errors } = await response.json();
    if (errors?.some(({ code }) => ['MANIFEST_UNKNOWN', 'NAME_UNKNOWN'].includes(code))) return null;
  }
  requireThat(response.ok, `Registry manifest lookup returned HTTP ${response.status}`);
  const bytes = Buffer.from(await response.arrayBuffer());
  const manifest = JSON.parse(bytes);
  const digest = response.headers.get('Docker-Content-Digest');
  requireThat(manifest.schemaVersion === 2 && digestPattern.test(manifest.config?.digest) && digestPattern.test(digest), 'Invalid registry image manifest');
  return { digest, config: manifest.config.digest, bytes, mediaType: response.headers.get('Content-Type')?.split(';')[0] };
}

export async function readManifest(image, token, request = fetch) {
  const manifest = await fetchManifest(image, token, request);
  return manifest && { digest: manifest.digest, config: manifest.config };
}

// The release job serializes publishers and selects the current Web release.
export async function promoteLatest(image, token, expectedDigest, request = fetch) {
  const match = imagePattern.exec(image);
  requireThat(match, 'Invalid versioned Web registry reference');
  const versioned = await fetchManifest(image, token, request);
  requireThat(versioned && (!expectedDigest || versioned.digest === expectedDigest), `Missing or changed versioned manifest for ${image}`);
  const { bytes, mediaType, digest } = versioned;
  requireThat(manifestTypes.includes(mediaType) && `sha256:${createHash('sha256').update(bytes).digest('hex')}` === digest, 'Versioned manifest bytes or media type do not match');
  const latest = `${host}/${match[1]}:latest`;
  if ((await readManifest(latest, token, request))?.digest !== digest) {
    const response = await request(`https://${host}/v2/${match[1]}/manifests/latest`, {
      method: 'PUT', headers: { Authorization: `Bearer ${token}`, 'Content-Type': mediaType },
      body: bytes, redirect: 'error', signal: timeout(),
    });
    requireThat(response.status === 201, `Publishing ${latest} returned HTTP ${response.status}`);
    requireThat((await readManifest(latest, token, request))?.digest === digest, `Registry readback mismatch for ${latest}`);
  }
  return digest;
}

export async function publishImage({ image, config, read, push }) {
  const existing = await read(image);
  if (existing) {
    requireThat(existing.config === config, `Refusing to overwrite ${image}`);
    return existing.digest;
  }
  await push(image);
  const published = await read(image);
  requireThat(published?.config === config, `Registry readback mismatch for ${image}`);
  return published.digest;
}

async function main(env) {
  const { VERSION: version, GITHUB_SHA: source, GITHUB_REPOSITORY: repo, GITHUB_ACTOR: actor, GH_TOKEN: password } = env;
  requireThat(env.GITHUB_ACTIONS === 'true' && env.GITHUB_REF === 'refs/heads/main' && ['push', 'workflow_dispatch'].includes(env.GITHUB_EVENT_NAME) && env.LOGS_PUBLISH_RELEASES === 'true', 'Only the enabled main release job may publish images');
  const image = `${host}/${repo?.toLowerCase()}:${version}`;
  const match = imagePattern.exec(image);
  requireThat(match && actor && password, 'Missing Web registry publication configuration');
  const tokenUrl = new URL(`https://${host}/token`);
  tokenUrl.search = new URLSearchParams({ service: host, scope: `repository:${match[1]}:pull,push` });
  const auth = await fetch(tokenUrl, { headers: { Authorization: `Basic ${Buffer.from(`${actor}:${password}`).toString('base64')}` }, redirect: 'error', signal: timeout() });
  requireThat(auth.ok, `Registry token request returned HTTP ${auth.status}`);
  const { token } = await auth.json();
  requireThat(typeof token === 'string' && token.length > 0, 'Missing registry token');
  if (env.WEB_REGISTRY_REUSE === 'true') {
    requireThat(env.GITHUB_EVENT_NAME === 'workflow_dispatch' && env.TARGET_COMPONENT === 'maintenance', 'Registry-only promotion requires maintenance');
    const digest = await promoteLatest(image, token);
    console.log(`Verified ${host}/${match[1]}:latest at ${digest}, matching ${image}`);
    return;
  }
  const directory = join('dist', 'web');
  const archive = inspect(directory, 'web', version, source).files[0].name;
  const local = `logs-web:${version}`;
  const configDir = mkdtempSync(join(tmpdir(), 'logs-docker-'));
  const docker = (args, input) => execFileSync('docker', args, { env: { ...env, DOCKER_CONFIG: configDir }, input, encoding: 'utf8', stdio: ['pipe', 'pipe', 'inherit'] }).trim();
  try {
    docker(['image', 'load', '--input', join(directory, archive)]);
    const [metadata] = JSON.parse(docker(['image', 'inspect', local]));
    const labels = metadata.Config.Labels;
    requireThat(metadata.Os === 'linux' && metadata.Architecture === 'amd64' && labels['org.opencontainers.image.revision'] === source && labels['org.opencontainers.image.version'] === version && digestPattern.test(metadata.Id), 'Docker image identity mismatch');

    const digest = await publishImage({
      image, config: metadata.Id,
      read: ref => readManifest(ref, token),
      push: ref => {
        docker(['login', host, '--username', actor, '--password-stdin'], `${password}\n`);
        docker(['image', 'tag', local, ref]);
        docker(['image', 'push', '--platform', 'linux/amd64', ref]);
      },
    });
    console.log(`Verified ${image} at ${digest} from ${source}`);
    await promoteLatest(image, token, digest);
    console.log(`Verified ${host}/${match[1]}:latest at ${digest}, matching ${image}`);
  } finally {
    rmSync(configDir, { recursive: true, force: true });
  }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try { await main(process.env); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
