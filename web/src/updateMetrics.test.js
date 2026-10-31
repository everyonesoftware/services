import assert from 'node:assert/strict'
import test from 'node:test'
import { countAvailableUpdates } from './updateMetrics.js'

test('counts containers with available updates across Compose groups', () => {
  const projects = [
    { updateAvailable: true, containers: [
      { updateAvailable: true },
      { updateAvailable: false },
    ] },
    { updateAvailable: true, containers: [
      { updateAvailable: true },
    ] },
  ]

  assert.equal(countAvailableUpdates(projects), 2)
})

test('returns zero when no containers have available updates', () => {
  assert.equal(countAvailableUpdates([
    { updateAvailable: false, containers: [{ updateAvailable: false }] },
  ]), 0)
  assert.equal(countAvailableUpdates([]), 0)
})
