import { expect, test } from 'vitest'
import { dagLayout } from './dagLayout'
import type { TaskDetail } from './api'

function task(name: string, deps: string[] = [], status: TaskDetail['status'] = 'pending'): TaskDetail {
  return {
    id: name,
    name,
    status,
    commands: [],
    env: {},
    requires: {},
    timeout_seconds: null,
    retries: 0,
    retry_count: 0,
    depends_on: deps.length ? deps : undefined,
  }
}

function layerOf(nodes: ReturnType<typeof dagLayout>['nodes'], name: string): number {
  return nodes.find((n) => n.name === name)!.layer
}

test('roots with no deps land in layer 0', () => {
  const { nodes } = dagLayout([task('a'), task('b')])
  expect(layerOf(nodes, 'a')).toBe(0)
  expect(layerOf(nodes, 'b')).toBe(0)
})

test('a chain a -> b -> c yields layers 0, 1, 2', () => {
  const { nodes } = dagLayout([task('a'), task('b', ['a']), task('c', ['b'])])
  expect(layerOf(nodes, 'a')).toBe(0)
  expect(layerOf(nodes, 'b')).toBe(1)
  expect(layerOf(nodes, 'c')).toBe(2)
})

test('a fan-in node lands one layer past its deepest predecessor', () => {
  const tasks = [
    task('frame-001'),
    task('frame-002'),
    task('setup'),
    task('frame-003', ['setup']),
    task('denoise-all', ['frame-001', 'frame-002', 'frame-003']),
  ]
  const { nodes } = dagLayout(tasks)
  // frame-003 depends on setup(0) so is layer 1; denoise-all's deepest dep is
  // frame-003(1), so denoise-all is layer 2.
  expect(layerOf(nodes, 'frame-003')).toBe(1)
  expect(layerOf(nodes, 'denoise-all')).toBe(2)
})

test('edges are dep -> task for every depends_on entry', () => {
  const { edges } = dagLayout([task('a'), task('b', ['a'])])
  expect(edges).toEqual([{ from: 'a', to: 'b' }])
})

test('ignores a depends_on name that is not a known task', () => {
  const { nodes, edges } = dagLayout([task('b', ['ghost'])])
  expect(layerOf(nodes, 'b')).toBe(0)
  expect(edges).toEqual([])
})

test('a two-cycle and a self-loop terminate without throwing and yield finite integer layers', () => {
  const tasks = [task('a', ['b', 'a']), task('b', ['a'])]
  const { nodes, edges } = dagLayout(tasks)

  // One node entry per task.
  expect(nodes).toHaveLength(2)
  expect(nodes.map((n) => n.name).sort()).toEqual(['a', 'b'])

  // Every layer is a finite, non-negative integer (no NaN/undefined/Infinity).
  for (const n of nodes) {
    expect(Number.isInteger(n.layer)).toBe(true)
    expect(Number.isFinite(n.layer)).toBe(true)
    expect(n.layer).toBeGreaterThanOrEqual(0)
  }

  // Edges are present for every depends_on entry, including the self-loop.
  expect(edges).toEqual(
    expect.arrayContaining([
      { from: 'b', to: 'a' },
      { from: 'a', to: 'a' },
      { from: 'a', to: 'b' },
    ]),
  )
  expect(edges).toHaveLength(3)
})
