import { memo } from 'react'
import { PillButton } from '../components/holo'
import { Input } from '../components/Input'
import { newKvRow, type KvRow } from './specBuilder'
import { useFocusAfterUpdate } from './useFocusAfterUpdate'

interface KeyValueRepeaterProps {
  // Ids are keyed by the owning row's identity, never by a position: an
  // index-keyed id re-associates every label below a removed row, so the control
  // a screen reader announces after a remove is not the one it names.
  idPrefix: string
  groupLabel: string
  itemNoun: string
  rows: KvRow[]
  onChange: (rows: KvRow[]) => void
  announce: (message: string) => void
}

// Two rows with the same key cannot both survive into a JSON object - the last
// one wins. That is a statement about this form's own encoding, not about any
// server rule, so it cannot drift against anything and it never blocks a submit.
function hasDuplicateKey(rows: KvRow[]): boolean {
  const seen = new Set<string>()
  for (const r of rows) {
    if (r.key === '') continue
    if (seen.has(r.key)) return true
    seen.add(r.key)
  }
  return false
}

// Memoized: an untouched row's `rows` array keeps its identity across an
// unrelated edit to the same task (env vs requires are separate array fields,
// and toSpec never spreads through the item elements themselves).
export const KeyValueRepeater = memo(function KeyValueRepeater({
  idPrefix,
  groupLabel,
  itemNoun,
  rows,
  onChange,
  announce,
}: KeyValueRepeaterProps) {
  const focusAfterUpdate = useFocusAfterUpdate()
  const addId = `${idPrefix}-add`

  function add() {
    const row = newKvRow()
    onChange([...rows, row])
    announce(`${itemNoun} ${rows.length + 1} added`)
    focusAfterUpdate(`${idPrefix}-${row.id}-key`)
  }

  function remove(i: number) {
    const gone = rows[i]
    onChange(rows.filter((r) => r.id !== gone.id))
    announce(`${itemNoun} ${gone.key === '' ? i + 1 : gone.key} removed`)
    const next = rows[i + 1] ?? rows[i - 1]
    focusAfterUpdate(next === undefined ? addId : `${idPrefix}-${next.id}-remove`)
  }

  function edit(i: number, patch: Partial<KvRow>) {
    onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  }

  return (
    <div role="group" aria-label={groupLabel} className="flex flex-col gap-1.5">
      <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute">{groupLabel}</span>
      {rows.map((row, i) => (
        <div key={row.id} className="flex flex-wrap items-center gap-1.5">
          <Input
            id={`${idPrefix}-${row.id}-key`}
            aria-label={`Key ${i + 1}`}
            value={row.key}
            spellCheck={false}
            onChange={(e) => edit(i, { key: e.target.value })}
            className="w-[40%] min-w-[8rem] font-mono"
          />
          <Input
            id={`${idPrefix}-${row.id}-value`}
            aria-label={`Value ${i + 1}`}
            value={row.value}
            spellCheck={false}
            onChange={(e) => edit(i, { value: e.target.value })}
            className="w-[40%] min-w-[8rem] font-mono"
          />
          <PillButton
            id={`${idPrefix}-${row.id}-remove`}
            // The index is folded into the named form too - two rows can share
            // a key (the last-one-wins note exists for exactly that case), and
            // a bare key alone would give both remove controls the same name.
            aria-label={`Remove ${itemNoun} ${row.key === '' ? i + 1 : `${i + 1}: ${row.key}`}`}
            onClick={() => remove(i)}
          >
            Remove
          </PillButton>
        </div>
      ))}
      {hasDuplicateKey(rows) ? (
        <p className="text-[11px] text-fg-dim">Two rows share a key; the last row wins.</p>
      ) : null}
      <div>
        <PillButton id={addId} onClick={add}>
          Add {itemNoun}
        </PillButton>
      </div>
    </div>
  )
})
