import type { TaskDetail } from './api'
import { GlassPanel } from '../components/holo'
import { taskStatusColor } from './taskStatus'
import { dagLayout, type DagNode } from './dagLayout'

const COL_W = 150
const ROW_H = 44
const NODE_W = 120
const NODE_H = 26
const PAD = 16

// Maps a node status to an SVG stroke/fill class. Reuses taskStatusColor's
// buckets; the text-* class doubles as an SVG `fill`/`stroke` via Tailwind's
// currentColor tokens on the wrapping <g>.
function nodeClass(node: DagNode): string {
  return taskStatusColor(node.status).text
}

// Visual-only dependency strip. The authoritative, screen-reader-navigable
// representation of dependencies is the tasks table's deps column; this SVG is
// an aid, so it is a single role="img" with a summarizing aria-label rather than
// individually focusable nodes.
export function TaskDag({ tasks }: { tasks: TaskDetail[] }) {
  if (tasks.length === 0) {
    return (
      <GlassPanel className="p-4 text-[12px] text-fg-mute">No tasks to graph.</GlassPanel>
    )
  }

  const { nodes, edges } = dagLayout(tasks)

  // Position: x by layer, y by order within a layer.
  const perLayer = new Map<number, number>()
  const pos = new Map<string, { x: number; y: number }>()
  for (const n of nodes) {
    const row = perLayer.get(n.layer) ?? 0
    perLayer.set(n.layer, row + 1)
    pos.set(n.name, { x: PAD + n.layer * COL_W, y: PAD + row * ROW_H })
  }

  const maxLayer = Math.max(...nodes.map((n) => n.layer))
  const maxRow = Math.max(...Array.from(perLayer.values()))
  const width = PAD * 2 + (maxLayer + 1) * COL_W
  const height = PAD * 2 + maxRow * ROW_H

  const label = `Task dependency graph: ${nodes.length} tasks, ${edges.length} dependency edges`

  return (
    <GlassPanel className="overflow-x-auto p-2">
      <svg role="img" aria-label={label} width={width} height={height} className="text-fg-mute">
        {edges.map((e, i) => {
          const from = pos.get(e.from)!
          const to = pos.get(e.to)!
          const done = nodes.find((n) => n.name === e.from)?.status === 'done'
          return (
            <line
              key={i}
              x1={from.x + NODE_W}
              y1={from.y + NODE_H / 2}
              x2={to.x}
              y2={to.y + NODE_H / 2}
              stroke="currentColor"
              strokeWidth={1}
              strokeDasharray={done ? undefined : '4 3'}
              opacity={0.6}
            />
          )
        })}
        {nodes.map((n) => {
          const p = pos.get(n.name)!
          return (
            <g key={n.name} className={nodeClass(n)}>
              <rect
                x={p.x}
                y={p.y}
                width={NODE_W}
                height={NODE_H}
                rx={5}
                fill="currentColor"
                fillOpacity={0.12}
                stroke="currentColor"
                strokeOpacity={0.6}
              />
              <text
                x={p.x + NODE_W / 2}
                y={p.y + NODE_H / 2 + 3}
                textAnchor="middle"
                className="fill-fg font-mono text-[10px]"
              >
                {n.name.length > 16 ? `${n.name.slice(0, 15)}…` : n.name}
              </text>
            </g>
          )
        })}
      </svg>
    </GlassPanel>
  )
}
