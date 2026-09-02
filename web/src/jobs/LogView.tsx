import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { Button } from '../components/Button'
import { PillButton } from '../components/holo'
import { MAX_LINES, preservedScrollTop, shouldFollow, type LogRow } from './logBuffer'
import type { LogStreamStatus, TaskLogStreamResult } from './useTaskLogStream'

// Status vocabulary for the header strip, replacing LogTab's old
// `STATIC · HISTORY` / `live tailing pending` (LogTab.tsx:37-40). LIVE appears
// ONLY while a stream is actually open - a badge on a non-streaming view would
// imply output we are not receiving.
function statusLabel(status: LogStreamStatus, attempt: number): string {
  switch (status) {
    case 'live':
      return 'LIVE'
    case 'loading':
      return 'LOADING'
    case 'recovering':
      return 'RECOVERING'
    case 'reconnecting':
      return `RECONNECTING (${attempt}/5)`
    case 'disconnected':
      return 'DISCONNECTED'
    case 'ended':
      return 'ENDED'
    case 'history':
      return 'HISTORY'
    case 'error':
      return 'ERROR'
    default:
      return 'IDLE'
  }
}

// UTC HH:MM:SS. Deliberately locale-independent, so the mono column is a fixed
// width and tests are deterministic.
function logTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().slice(11, 19)
}

function LogRowView({ row }: { row: LogRow }) {
  if (row.kind === 'marker') {
    return (
      <div data-kind="marker" className="my-1 border-y border-warn/40 py-0.5 text-warn">
        --- {row.text} ---
      </div>
    )
  }
  // Two columns, not the hi-fi's four (hifi3-holo-pages.jsx:2754-2759): a
  // logEntry carries no level and no source (internal/api/tasks.go:56-61), so an
  // INFO/DEBUG column would invent data.
  return (
    <div data-kind={row.kind} className="grid grid-cols-[62px_1fr] gap-3">
      <span className="text-fg-dim">{logTime(row.time)}</span>
      {/* whitespace-pre-wrap keeps indentation. Content is always a React text
          child, which escapes it: this is the XSS boundary, and a job printing
          <img onerror> must render as characters. NEVER dangerouslySetInnerHTML -
          this is untrusted subprocess output. */}
      <span
        className={`whitespace-pre-wrap break-all ${row.stream === 'stderr' ? 'text-err' : 'text-fg'}`}
      >
        {row.text}
      </span>
    </div>
  )
}

export interface LogViewProps {
  stream: TaskLogStreamResult
  /** e.g. `/v1/events?task_id=<id> · single-task stream` on the full-screen view. */
  endpointCaption?: string
  /** Extra header content, e.g. the job-detail tab's "Full screen" link. */
  headerExtra?: ReactNode
  bodyClassName?: string
  /** Test seam: called whenever the view scrolls itself to the bottom. */
  onScrolledToBottom?: () => void
  /** Test seam: called with the scrollTop applied after content was added above. */
  onPrependAdjust?: (scrollTop: number) => void
}

export function LogView({
  stream,
  endpointCaption,
  headerExtra,
  bodyClassName,
  onScrolledToBottom,
  onPrependAdjust,
}: LogViewProps) {
  const { rows, status, attempt, evicted, historyTruncated, total, errorMessage } = stream
  const { canLoadEarlier, loadingEarlier, earlierComplete } = stream
  const [follow, setFollow] = useState(true)
  const boxRef = useRef<HTMLDivElement>(null)

  const scrollToBottom = useCallback(() => {
    const el = boxRef.current
    if (el) el.scrollTop = el.scrollHeight
    onScrolledToBottom?.()
  }, [onScrolledToBottom])

  useEffect(() => {
    if (follow) scrollToBottom()
  }, [rows, follow, scrollToBottom])

  function handleScroll() {
    const el = boxRef.current
    if (el) setFollow(shouldFollow(el.scrollTop, el.scrollHeight, el.clientHeight))
  }

  const prevFirstKey = useRef<number | undefined>(undefined)
  const prevHeight = useRef(0)

  useLayoutEffect(() => {
    const firstKey = rows[0]?.key
    const changedAtTop = prevFirstKey.current !== undefined && firstKey !== prevFirstKey.current
    prevFirstKey.current = firstKey
    const el = boxRef.current
    if (!el) return
    const before = prevHeight.current
    prevHeight.current = el.scrollHeight
    // Only when content changed ABOVE the viewport and the user is reading
    // history rather than following the tail. A prepend gives its rows fresh
    // keys, so the first row's key moving is the signal; an append never
    // touches it.
    if (!changedAtTop || follow) return
    el.scrollTop = preservedScrollTop(el.scrollTop, before, el.scrollHeight)
    onPrependAdjust?.(el.scrollTop)
  }, [rows, follow, onPrependAdjust])

  const live = status === 'live'

  // Four-way, first match wins. MAX_LINES and rows count reassembled LINES;
  // `total` counts server-side log ENTRIES - two different units (code review,
  // L5) - so each notice names them separately rather than implying a single
  // "N of M" count of the same thing. The forward-walk notice can now only
  // appear after a recovery that hit MAX_BACKFILL_PAGES, so it resolves last.
  let notice: string | null = null
  if (evicted) {
    notice = 'Earlier output not shown.'
  } else if (!earlierComplete && rows.length > 0) {
    notice = `Showing the most recent ${rows.length.toLocaleString('en-US')} lines of ${total.toLocaleString('en-US')} log entries.`
  } else if (historyTruncated) {
    notice = `Showing the first ${MAX_LINES.toLocaleString('en-US')} of ${total.toLocaleString('en-US')} log entries. Live output continues below.`
  }

  let body: ReactNode
  if (status === 'error') {
    body = (
      <div className="flex flex-col items-start gap-2 p-1">
        <div className="text-[12px] text-err">
          Failed to load logs.{errorMessage ? ` ${errorMessage}` : ''}
        </div>
        <Button className="w-auto px-4" onClick={stream.reconnect}>
          Retry
        </Button>
      </div>
    )
  } else if (status === 'loading' && rows.length === 0) {
    body = <div className="p-1 text-[12px] text-fg-mute">Loading logs...</div>
  } else if (rows.length === 0) {
    body = <div className="p-1 text-[12px] text-fg-mute">No log output.</div>
  } else {
    body = rows.map((r) => <LogRowView key={r.key} row={r} />)
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-3 py-2 font-mono text-[10px] tracking-[0.14em] text-fg-mute">
        <span className={`flex items-center gap-1.5 ${live ? 'text-ok' : 'text-fg-dim'}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${live ? 'bg-ok' : 'bg-fg-mute'}`} />
          {statusLabel(status, attempt)}
        </span>
        {endpointCaption && (
          <span className="truncate tracking-[0.06em]">{endpointCaption}</span>
        )}
        <span className="ml-auto flex items-center gap-2">
          {!follow && (
            <PillButton
              className="!px-3 !py-1 !text-[10px]"
              onClick={() => {
                setFollow(true)
                scrollToBottom()
              }}
            >
              Jump to latest
            </PillButton>
          )}
          <PillButton
            aria-pressed={follow}
            variant={follow ? 'primary' : 'ghost'}
            className="!px-3 !py-1 !text-[10px]"
            onClick={() => {
              const next = !follow
              setFollow(next)
              if (next) scrollToBottom()
            }}
          >
            Follow tail
          </PillButton>
          {headerExtra}
        </span>
      </div>

      {status === 'disconnected' && (
        <div className="flex flex-wrap items-center gap-3 border-b border-border bg-warn/5 px-3 py-2 text-[11px] text-warn">
          <span>Disconnected after 5 attempts.</span>
          <PillButton className="!px-3 !py-1 !text-[10px]" onClick={stream.reconnect}>
            Reconnect
          </PillButton>
        </div>
      )}

      {notice && (
        <div className="border-b border-border px-3 py-2 text-[11px] text-fg-mute">{notice}</div>
      )}

      <div
        ref={boxRef}
        data-testid="log-body"
        onScroll={handleScroll}
        className={`flex flex-col gap-0.5 bg-black/25 p-3 font-mono text-[11px] ${
          bodyClassName ?? 'max-h-[420px] overflow-auto'
        }`}
      >
        {loadingEarlier ? (
          <div className="pb-1 text-[11px] text-fg-mute">Loading earlier...</div>
        ) : canLoadEarlier ? (
          <div className="pb-1">
            <PillButton className="!px-3 !py-1 !text-[10px]" onClick={stream.loadEarlier}>
              Load earlier
            </PillButton>
          </div>
        ) : null}
        {body}
      </div>
    </div>
  )
}
