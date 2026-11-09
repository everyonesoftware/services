import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
const workflow = (await readFile(new URL('../.github/workflows/verify-and-publish.yml', import.meta.url), 'utf8')).replaceAll('\r\n', '\n');
const job = name => workflow.match(new RegExp(`^  ${name}:\\n[\\s\\S]*?(?=^  [a-z][\\w-]*:|$(?![\\s\\S]))`, 'm'))?.[0] ?? '';

test('serializes whole publish and notify lifecycle without blocking unrelated PRs', () => {
  const concurrency = workflow.slice(workflow.indexOf('\nconcurrency:'), workflow.indexOf('\njobs:'));
  assert.match(concurrency, /github\.event_name == 'pull_request'/);
  assert.match(concurrency, /github\.event\.pull_request\.number/);
  assert.match(concurrency, /services-publish-/);
  assert.match(concurrency, /cancel-in-progress: false/);
  assert.match(concurrency, /queue: max/);
});

test('preserves build gates and captures root action digest immediately after successful push', () => {
  assert.match(job('verify'), /go test \.\/\.\.\./);
  assert.match(job('verify'), /npm test/);
  assert.match(job('verify'), /npm run build/);
  assert.match(job('verify'), /node --test scripts\/\*\.test\.mjs/);
  const publish = job('publish');
  assert.match(publish, /if: github\.event_name != 'pull_request'/);
  assert.match(publish, /id: push\n\s+uses: docker\/build-push-action@v7/);
  assert.match(publish, /labels: \$\{\{ steps\.meta\.outputs\.labels \}\}\n\n      - name: Capture/);
  assert.match(publish, /PUBLISHED_DIGEST: \$\{\{ steps\.push\.outputs\.digest \}\}/);
  assert.match(publish, /PUBLISHED_TAGS: \$\{\{ steps\.meta\.outputs\.tags \}\}/);
  assert.match(publish, /APP_VERSION: \$\{\{ needs\.verify\.outputs\.version \}\}/);
  assert.match(publish, /uses: actions\/upload-artifact@/);
  assert.match(publish, /notification_artifact: \$\{\{ steps\.capture\.outputs\.artifact \}\}/);
  assert.match(publish, /continue-on-error: true/g);
  assert.doesNotMatch(publish, /imagetools|manifest inspect|curl/);
});

test('notification is opt-in after publish and uses persisted payload through federated Tailscale', () => {
  const notify = job('notify');
  assert.match(notify, /needs: publish/);
  assert.match(notify, /github\.event_name != 'pull_request'/);
  assert.match(notify, /needs\.publish\.result == 'success'/);
  assert.match(notify, /vars\.SERVICES_PUBLISH_NOTIFICATIONS == 'true'/);
  assert.match(notify, /id-token: write/);
  assert.match(notify, /uses: tailscale\/github-action@v4/);
  for (const [input, variable] of [['oauth-client-id', 'TAILSCALE_CLIENT_ID'], ['audience', 'TAILSCALE_AUDIENCE'], ['tags', 'TAILSCALE_TAGS']]) {
    assert.ok(notify.includes(`${input}: \${{ vars.${variable} }}`));
  }
  assert.match(notify, /uses: actions\/download-artifact@/);
  assert.match(notify, /name: \$\{\{ needs\.publish\.outputs\.notification_artifact \}\}/);
  assert.match(notify, /SERVICES_PUBLISH_TOKEN: \$\{\{ secrets\.SERVICES_PUBLISH_TOKEN \}\}/);
  assert.match(notify, /SERVICES_PUBLISH_URL: \$\{\{ vars\.SERVICES_PUBLISH_URL \}\}/);
  assert.match(notify, /node scripts\/publish-notification\.mjs send/);
  assert.doesNotMatch(notify, /mjs capture|oauth-secret|authkey|packages: write/);
  assert.match(job('notification-failure'), /always\(\)/);
  assert.match(job('notification-failure'), /needs\.notify\.result == 'failure'/);
  assert.match(job('notification-failure'), /needs\.publish\.result == 'success'/);
  assert.match(job('notification-failure'), /exit 1/);
});
