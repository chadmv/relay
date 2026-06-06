import type { Worker } from './api'

function formatRevokedAt(iso?: string): string {
  if (!iso) return 'unknown'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? 'unknown' : d.toLocaleString()
}

export function RevokedWorkersTable({ workers }: { workers: Worker[] }) {
  if (workers.length === 0) {
    return (
      <div className="mx-auto mt-10 max-w-md rounded-card border border-border bg-white/5 p-6 text-center text-[13px] text-fg-mute">
        No revoked workers.
      </div>
    )
  }
  return (
    <table className="w-full text-left text-[13px]">
      <thead className="font-mono text-[11px] text-fg-mute">
        <tr>
          <th className="py-2 pr-4">NAME</th>
          <th className="py-2 pr-4">HOSTNAME</th>
          <th className="py-2 pr-4">REVOKED AT</th>
        </tr>
      </thead>
      <tbody>
        {workers.map((w) => (
          <tr key={w.id} className="border-t border-border">
            <td className="py-2 pr-4">{w.name}</td>
            <td className="py-2 pr-4 text-fg-mute">{w.hostname}</td>
            <td className="py-2 pr-4 text-fg-mute">{formatRevokedAt(w.revoked_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
