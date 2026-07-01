import { expect, test } from 'vitest'
import { STARTER_TEMPLATE, validateSpecText } from './specTemplate'

test('the starter template is valid JSON with a name and a non-empty tasks array', () => {
  const r = validateSpecText(STARTER_TEMPLATE)
  expect(r.ok).toBe(true)
  if (r.ok) {
    expect(r.value).toMatchObject({ name: 'my-job' })
    expect(Array.isArray((r.value as { tasks: unknown[] }).tasks)).toBe(true)
    expect((r.value as { tasks: unknown[] }).tasks.length).toBeGreaterThan(0)
  }
})

test('a valid minimal spec passes', () => {
  const r = validateSpecText('{"name":"x","tasks":[{"name":"t","command":["echo"]}]}')
  expect(r.ok).toBe(true)
})

test('malformed JSON fails with an Invalid JSON message', () => {
  const r = validateSpecText('{ not json }')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/Invalid JSON/)
})

test('missing name fails with a targeted message', () => {
  const r = validateSpecText('{"tasks":[{"name":"t","command":["echo"]}]}')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/name/i)
})

test('empty name fails', () => {
  const r = validateSpecText('{"name":"","tasks":[{"name":"t"}]}')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/name/i)
})

test('empty tasks array fails with a targeted message', () => {
  const r = validateSpecText('{"name":"x","tasks":[]}')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/task/i)
})

test('missing tasks fails', () => {
  const r = validateSpecText('{"name":"x"}')
  expect(r.ok).toBe(false)
  if (!r.ok) expect(r.error).toMatch(/task/i)
})
