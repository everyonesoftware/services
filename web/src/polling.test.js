import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { transformWithOxc } from 'vite'

// Exercise the real hook without a DOM; explicitly separate render from effects.
async function pollingHarness() {
  const source = readFileSync(new URL('./App.tsx', import.meta.url), 'utf8')
  const hook = source.slice(source.indexOf('const errorMessage'), source.indexOf('const timeLabel')) + source.slice(source.indexOf('function usePolling'), source.indexOf('type AppProps'))
  const slots = []
  const effects = []
  const requests = []
  let cursor = 0
  const changed = (a, b) => !a || a.some((value, i) => !Object.is(value, b[i]))
  const useState = initial => {
    const i = cursor++
    if (!(i in slots)) slots[i] = typeof initial === 'function' ? initial() : initial
    return [slots[i], next => { slots[i] = typeof next === 'function' ? next(slots[i]) : next }]
  }
  const useRef = value => {
    const i = cursor++
    return slots[i] ??= { current: value }
  }
  const useCallback = (callback, deps) => {
    const i = cursor++
    if (!slots[i] || changed(slots[i].deps, deps)) slots[i] = { value: callback, deps }
    return slots[i].value
  }
  const useEffect = (effect, deps) => {
    const i = cursor++
    if (!slots[i] || changed(slots[i].deps, deps)) effects.push(() => {
      slots[i]?.cleanup?.()
      slots[i] = { deps, cleanup: effect() }
    })
  }
  const fetch = () => new Promise((resolve, reject) => requests.push({ resolve: data => resolve({ ok: true, json: async () => data }), reject }))
  const window = { setTimeout: () => 0, clearTimeout() {}, setInterval: () => 0, clearInterval() {} }
  const { code } = await transformWithOxc(hook, 'polling.ts')
  const runPolling = new Function('useState', 'useRef', 'useCallback', 'useEffect', 'fetch', 'window', `${code}; return usePolling`)(useState, useRef, useCallback, useEffect, fetch, window)
  return {
    requests,
    render(key) { cursor = 0; return runPolling('/api/projects', 30000, false, undefined, key) },
    effects() { effects.splice(0).forEach(effect => effect()) },
  }
}
const settle = () => new Promise(resolve => setImmediate(resolve))

test('refresh key gates retained inventory on the very first render before effects', async () => {
  const harness = await pollingHarness()
  harness.render(''); harness.effects()
  harness.requests[0].resolve({ projects: ['stale'] }); await settle()
  assert.deepEqual(harness.render('').data, { projects: ['stale'] })
  const terminal = harness.render('complete-1')
  assert.equal(terminal.refreshing, true, 'terminal observation must synchronously hide stale actions')
  assert.notEqual(terminal.dataKey, 'complete-1')
  harness.effects()
  assert.equal(harness.requests.length, 2, 'a key change starts fresh inventory immediately')
  harness.requests[1].resolve({ projects: ['newer update'] }); await settle()
  const fresh = harness.render('complete-1')
  assert.equal(fresh.refreshing, false)
  assert.equal(fresh.dataKey, 'complete-1')
  assert.deepEqual(fresh.data, { projects: ['newer update'] })
})

test('old in-flight success cannot bless a terminal key and failed reconciliation retains the gate and error', async () => {
  const harness = await pollingHarness()
  harness.render(''); harness.effects()
  harness.render('complete-1')
  // Resolve before effect cleanup: the response still belongs to the old key.
  harness.requests[0].resolve({ projects: ['stale'] }); await settle()
  assert.equal(harness.render('complete-1').refreshing, true)
  harness.effects()
  harness.requests.at(-1).reject(new Error('Inventory unavailable')); await settle()
  const failed = harness.render('complete-1')
  assert.equal(failed.refreshing, true)
  assert.equal(failed.error, 'Inventory unavailable')
  assert.notEqual(failed.dataKey, 'complete-1')
})
