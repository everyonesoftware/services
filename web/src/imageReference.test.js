import assert from 'node:assert/strict'
import test from 'node:test'
import { splitImageReference } from './imageReference.js'

test('separates an image name from its configured tag', () => {
  assert.deepEqual(splitImageReference('ghcr.io/example/app:stable'), {
    image: 'ghcr.io/example/app',
    tag: 'stable',
  })
})

test('defaults an untagged image reference to latest', () => {
  assert.deepEqual(splitImageReference('example/app'), {
    image: 'example/app',
    tag: 'latest',
  })
})

test('shows a shortened digest when the container was started by image ID', () => {
  assert.deepEqual(splitImageReference('sha256:0123456789abcdef0123456789abcdef'), {
    image: 'Image ID',
    tag: 'sha256:0123456789ab…',
  })
})

test('shows a shortened digest for digest-pinned references', () => {
  assert.deepEqual(splitImageReference('example/app@sha256:0123456789abcdef0123456789abcdef'), {
    image: 'example/app',
    tag: '@sha256:0123456789ab…',
  })
})
