import type { JobSort } from './api'

const OPTIONS: { value: JobSort; label: string }[] = [
  { value: '-created_at', label: 'Newest' },
  { value: 'created_at', label: 'Oldest' },
  { value: 'name', label: 'Name A→Z' },
  { value: '-name', label: 'Name Z→A' },
  { value: '-priority', label: 'Priority high→low' },
  { value: 'priority', label: 'Priority low→high' },
  { value: 'status', label: 'Status A→Z' },
  { value: '-status', label: 'Status Z→A' },
  { value: '-updated_at', label: 'Recently updated' },
  { value: 'updated_at', label: 'Least recently updated' },
]

export function SortControl({
  value,
  onChange,
  disabled,
  disabledHint,
}: {
  value: JobSort
  onChange: (sort: JobSort) => void
  disabled?: boolean
  disabledHint?: string
}) {
  return (
    <select
      aria-label="Sort jobs"
      title={disabled ? disabledHint : undefined}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value as JobSort)}
      className="rounded-full border border-border bg-black/25 px-3 py-1.5 font-sans text-[12px] text-fg outline-none disabled:opacity-50"
    >
      {OPTIONS.map((o) => (
        <option key={o.value} value={o.value} className="bg-bg text-fg">
          {o.label}
        </option>
      ))}
    </select>
  )
}
