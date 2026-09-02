import { useState } from 'react'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { Chip, Table, TableCell, TableRow, type TableColumn } from '../components/holo'
import { formatRelativeTime } from './liveness'
import { useWorkerActions } from './useWorkerActions'
import { useWorkerWorkspaces } from './useWorkerWorkspaces'

const COLS = 'grid-cols-[120px_90px_1fr_120px_90px_90px]'
// Fixed tracks total 510px and this sits in a detail-page column of about 614px at
// 1280, so 600 is deliberately tight: it is the largest value that does not put a
// scrollbar on a maximized desktop window. Task 7 measures this one specifically.
const MIN_W = 'min-w-[600px]'

// ONE literal for the panel title and the table's accessible name. They were two
// hand-kept-equal strings in two files; the structural test on WorkerDetailPage
// (`every table on the page is named by its own panel title`) pins the RENDERED
// pair, since a test comparing two references to this constant could not fail.
export const WORKSPACES_PANEL_TITLE = 'Source workspaces'

const HEADERS: TableColumn[] = [
  { label: 'SHORT ID' },
  { label: 'TYPE' },
  { label: 'SOURCE KEY' },
  { label: 'BASELINE' },
  { label: 'LAST USED' },
  { label: 'ACTIONS', align: 'right' },
]

// Admin-only source workspaces table with per-row evict. Rendered inside the
// page's Panel (which supplies the glass frame and the "Source workspaces"
// title), so this component is only the header row + data rows + confirm flow.
// Mounted by WorkerDetailPage only for admins, so no inner is_admin check is
// needed. Eviction is best-effort/async (202): the row does not vanish
// immediately; the 15s workspace poll reconciles once the agent confirms.
export function WorkspacesPanel({ workerId }: { workerId: string }) {
  const { data, isLoading } = useWorkerWorkspaces(workerId)
  const { evict } = useWorkerActions(workerId)
  const [confirmId, setConfirmId] = useState<string | null>(null)
  const rows = data ?? []

  function runEvict() {
    if (confirmId) evict.mutate(confirmId)
    setConfirmId(null)
  }

  return (
    <div className="flex flex-col">
      <Table label={WORKSPACES_PANEL_TITLE} columns={COLS} minWidth={MIN_W} headers={HEADERS} headerClassName="px-4 py-2 tracking-wider">
        {rows.map((ws) => (
          <TableRow key={ws.short_id} className="border-b border-border/40 px-4 py-2 font-mono text-[11px]">
            <TableCell className="text-fg">{ws.short_id}</TableCell>
            <TableCell className="text-fg-mute">{ws.source_type}</TableCell>
            <TableCell className="truncate text-fg-mute">{ws.source_key}</TableCell>
            <TableCell className="text-fg-mute">{ws.baseline_hash}</TableCell>
            <TableCell className="text-fg-mute">{formatRelativeTime(ws.last_used_at)}</TableCell>
            <TableCell className="flex justify-end">
              <Chip tone="accent" onClick={evict.isPending ? undefined : () => setConfirmId(ws.short_id)}>
                Evict
              </Chip>
            </TableCell>
          </TableRow>
        ))}
      </Table>

      {/* The empty state, the error banner and the dialog are siblings of the table,
          never children: none of them is a valid child of role="table". The empty
          state only renders when there are no rows, so it still appears directly
          below the header row. */}
      {!isLoading && rows.length === 0 && (
        <div className="px-4 py-3 text-[12px] text-fg-mute">No workspaces.</div>
      )}

      {evict.error ? (
        <div className="mx-4 my-2 rounded-card border border-err/40 bg-err/10 px-4 py-2 text-[12px] text-err">
          {(evict.error as Error).message}
        </div>
      ) : null}

      {confirmId && (
        <ConfirmDialog
          title={`Evict workspace ${confirmId}?`}
          body="The agent removes it on next opportunity. A held workspace is refused."
          confirmLabel="Evict"
          onConfirm={runEvict}
          onCancel={() => setConfirmId(null)}
        />
      )}
    </div>
  )
}
