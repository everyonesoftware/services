import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { iconForImage, iconForProject } from './brandIcons.js'
const siJellyfin = iconForImage('jellyfin/jellyfin')
const siTailscale = iconForImage('tailscale/tailscale')

test('recognized apps use bundled upstream image assets instead of Simple Icons paths', () => {
  for (const [image, slug] of [['jellyfin/jellyfin', 'jellyfin'], ['tailscale/tailscale', 'tailscale'], ['louislam/uptime-kuma', 'uptimekuma']]) {
    const icon = iconForImage(image)
    assert.equal(icon.src, `/icons/${slug}.png`)
    assert.equal(icon.path, undefined)
    assert.equal(icon.hex, undefined)
  }
})

test('every supported application has a real bundled PNG image', () => {
  for (const image of ['jellyfin', 'plex', 'homeassistant', 'immich', 'nextcloud', 'traefik', 'grafana', 'prometheus', 'portainer', 'sonarr', 'radarr', 'adguard', 'uptime-kuma', 'qbittorrent', 'nginx', 'tailscale']) {
    const icon = iconForImage(image)
    assert.ok(icon, image)
    const bytes = readFileSync(new URL(`../public${icon.src}`, import.meta.url))
    assert.equal(bytes.subarray(0, 8).toString('hex'), '89504e470d0a1a0a', image)
    assert.ok(bytes.readUInt32BE(16) >= 32 && bytes.readUInt32BE(20) >= 32, image)
  }
})

test('recognizes a Jellyfin image reference', () => {
  assert.equal(iconForImage('ghcr.io/linuxserver/jellyfin:latest'), siJellyfin)
})

test('selects a recognized icon from the images in a Compose project', () => {
  const icon = iconForProject({ containers: [
    { image: 'custom/helper:1' },
    { image: 'jellyfin/jellyfin:10' },
  ] })
  assert.equal(icon, siJellyfin)
})

test('returns no brand icon for unknown images', () => {
  assert.equal(iconForImage('example/image:latest'), undefined)
})

test('recognizes Tailscale independently of tags, digests and registry ports', () => {
  for (const image of ['tailscale/tailscale:stable', 'ghcr.io/tailscale/tailscale@sha256:abc', 'registry.local:5000/tailscale/tailscale:latest']) {
    assert.equal(iconForImage(image), siTailscale)
  }
})

test('matches repository names, not brand strings in namespaces, tags or unrelated names', () => {
  for (const image of ['not-jellyfin/server:latest', 'custom/helper:jellyfin', 'custom/notjellyfin:latest', 'custom/jellyfin-backup:latest', '', 'sha256:abc']) {
    assert.equal(iconForImage(image), undefined, image)
  }
})

test('project uses application logo instead of its Tailscale sidecar regardless of order', () => {
  const containers = [{ image: 'tailscale/tailscale:stable' }, { image: 'jellyfin/jellyfin:latest' }]
  assert.equal(iconForProject({ name: 'media', containers }), siJellyfin)
  assert.equal(iconForProject({ name: 'media', containers: [...containers].reverse() }), siJellyfin)
  assert.equal(iconForProject({ name: 'vpn', containers: [containers[0]] }), siTailscale)
})

test('ambiguous and standalone projects have no misleading brand', () => {
  const containers = [{ image: 'jellyfin/jellyfin:latest' }, { image: 'plexinc/pms-docker:latest' }]
  assert.equal(iconForProject({ name: 'media', containers }), undefined)
  assert.equal(iconForProject({ name: 'Standalone', containers: [containers[0]] }), undefined)
  assert.equal(iconForProject({ name: 'custom', containers: [{ image: 'custom/app' }, { image: 'tailscale/tailscale' }] }), undefined)
})
