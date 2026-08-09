import { createContext, useContext } from 'react'
import type { ElementType, ReactNode } from 'react'

// Semantic wrapper set for the app's CSS-grid pseudo-tables. It owns the ARIA
// roles, the aria-label, aria-sort, the sort button and its caret, and the grid
// template - which travels on a context so the header row and the body rows cannot
// be put out of agreement by hand.
//
// It deliberately renders NO frame. The caller keeps its own wrapper, which makes
// the migration visually neutral across four different frame styles and keeps
// footers, error banners and dialogs inside the visual surface but OUTSIDE the
// role="table" subtree, where they would be invalid children.
//
// The base strings below contain ONLY utilities that are byte-identical across all
// eight consumers. Two competing Tailwind utilities on one element resolve by
// stylesheet order, not by class-attribute order, so a caller className cannot
// reliably override a base class: anything that varies is caller-supplied, never
// override-supplied. Class strings are literals so Tailwind v4's static scan emits
// them.
const HEADER_BASE = 'border-b border-border font-mono text-[10px] text-fg-mute'
const ROW_BASE = 'items-center'

export type SortDirection = 'ascending' | 'descending' | 'none'

// One definition, replacing the four duplicated pairs in WorkersTable, UsersTable,
// EnrollmentsTable and ReservationsTable. `sort` is the wire-format sort value: the
// field name, optionally '-'-prefixed for descending. Field names contain
// underscores and never hyphens, so stripping the first '-' is exactly "strip the
// descending prefix" - this is the behavior of the four helpers it replaces, kept
// byte-for-byte so the five already-roled tables are unchanged.
export function ariaSort(field: string, sort: string): SortDirection {
  if (sort.replace('-', '') !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
}

export function sortCaret(field: string, sort: string): string {
  if (sort.replace('-', '') !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

export interface TableColumn<F extends string = string> {
  label: string
  // Present => sortable: renders a button, a caret and aria-sort.
  field?: F
  align?: 'right'
  className?: string
}

interface TableProps<F extends string> {
  // Required: becomes the accessible name of the table.
  label: string
  // The `grid-cols-[...]` literal only; Table prepends `grid`. The literal must stay
  // in the consumer file for Tailwind v4's static scan, but it is declared once
  // there instead of being applied to two elements by hand.
  columns: string
  headers: TableColumn<F>[]
  sort?: string
  onSort?: (field: F) => void
  // The caller's header spacing and tracking deltas.
  headerClassName?: string
  className?: string
  children?: ReactNode
}

// The value is the raw columns string, never a fresh object literal, so it is
// referentially stable across renders.
const ColumnsContext = createContext<string | null>(null)

export function Table<F extends string = string>({
  label,
  columns,
  headers,
  sort = '',
  onSort,
  headerClassName,
  className,
  children,
}: TableProps<F>) {
  return (
    <ColumnsContext.Provider value={columns}>
      <div role="table" aria-label={label} className={className}>
        <div role="row" className={`grid ${columns} ${HEADER_BASE} ${headerClassName ?? ''}`}>
          {headers.map((h) => {
            const cls = [h.align === 'right' ? 'text-right' : '', h.className ?? ''].filter(Boolean).join(' ')
            const field = h.field
            if (field === undefined) {
              // No aria-sort on a static header: it would advertise a sort
              // affordance that does not exist.
              return (
                <span key={h.label} role="columnheader" className={cls || undefined}>
                  {h.label}
                </span>
              )
            }
            return (
              <div
                key={h.label}
                role="columnheader"
                aria-sort={ariaSort(field, sort)}
                className={cls || undefined}
              >
                <button type="button" className="text-left" onClick={() => onSort?.(field)}>
                  {h.label}
                  {sortCaret(field, sort)}
                </button>
              </div>
            )
          })}
        </div>
        {/* The caller's rows. No role="rowgroup": ARIA permits row children directly
            under table, and a rowgroup would force this component to wrap rows in an
            element it owns. */}
        {children}
      </div>
    </ColumnsContext.Provider>
  )
}

interface TableRowProps {
  as?: ElementType
  className?: string
  children?: ReactNode
  [prop: string]: unknown
}

export function TableRow({ as, className, children, ...rest }: TableRowProps) {
  const columns = useContext(ColumnsContext)
  // A silent fallback would ship as a mangled layout in production. A throw is
  // unconditional, so it surfaces in the first test render instead.
  if (columns === null) throw new Error('TableRow must be rendered inside a Table')
  const Tag = as ?? 'div'
  return (
    <Tag role="row" className={`grid ${columns} ${ROW_BASE} ${className ?? ''}`} {...rest}>
      {children}
    </Tag>
  )
}

interface TableCellProps {
  className?: string
  children?: ReactNode
  [prop: string]: unknown
}

export function TableCell({ className, children, ...rest }: TableCellProps) {
  return (
    <span role="cell" className={className} {...rest}>
      {children}
    </span>
  )
}
