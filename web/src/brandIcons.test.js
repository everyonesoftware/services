import assert from 'node:assert/strict'
import test from 'node:test'
import { iconForImage, iconForProject } from './brandIcons.js'
import { siJellyfin } from 'simple-icons'

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
