import type { TaskDetail, TaskStatus } from './api'

export interface DagNode {
  name: string
  status: TaskStatus
  layer: number
}

export interface DagEdge {
  from: string
  to: string
}

export interface DagLayout {
  nodes: DagNode[]
  edges: DagEdge[]
}

// Builds a small directed graph from tasks[].name + depends_on. Nodes get a
// longest-path layer index (roots at 0, a node = 1 + max(layer of its known
// deps)); edges point dep -> task. Unknown dep names are ignored so a partial or
// malformed dependency never crashes rendering. Cycles are not expected (the API
// forbids them); a defensive visited-guard bounds the recursion regardless.
export function dagLayout(tasks: TaskDetail[]): DagLayout {
  const byName = new Map<string, TaskStatus>()
  for (const t of tasks) byName.set(t.name, t.status)

  const depsOf = new Map<string, string[]>()
  for (const t of tasks) {
    const known = (t.depends_on ?? []).filter((d) => byName.has(d))
    depsOf.set(t.name, known)
  }

  const layerCache = new Map<string, number>()
  function layer(name: string, stack: Set<string>): number {
    const cached = layerCache.get(name)
    if (cached !== undefined) return cached
    if (stack.has(name)) return 0 // defensive cycle break
    stack.add(name)
    const deps = depsOf.get(name) ?? []
    const l = deps.length === 0 ? 0 : 1 + Math.max(...deps.map((d) => layer(d, stack)))
    stack.delete(name)
    layerCache.set(name, l)
    return l
  }

  const nodes: DagNode[] = tasks.map((t) => ({
    name: t.name,
    status: t.status,
    layer: layer(t.name, new Set()),
  }))

  const edges: DagEdge[] = []
  for (const t of tasks) {
    for (const dep of depsOf.get(t.name) ?? []) {
      edges.push({ from: dep, to: t.name })
    }
  }

  return { nodes, edges }
}
