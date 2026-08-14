import { createContext, useContext } from 'react'
import type { ComponentPropsWithoutRef, ElementType, ReactNode } from 'react'

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
// THE NARROW-VIEWPORT CONVENTION FOR TABLES (2026-08-13). A consumer whose template
// has fixed px tracks passes `minWidth` (a Tailwind min-w-[NNNpx] literal, sized at
// or above the sum of its own fixed tracks). That does two things, and both are
// required: it publishes ONE min-width onto the header row and every body row, and
// it wraps the role="table" subtree in an overflow-x-auto div so the table scrolls
// inside the caller's frame instead of widening the document.
//
// Why both. With negative free space an `fr` track falls back to its CONTENT
// minimum; the header row and the body rows are separate grid containers with
// different content, so they desynchronize. The min-width keeps free space
// non-negative so `fr` resolves identically in both - the same agreement guarantee
// this component already gives for `columns`.
//
// The wrapper is not the frame this primitive refuses to be: overflow only, no
// border, background, padding or radius, and it wraps ONLY the role="table"
// subtree, so the footers, error banners and dialogs that every consumer renders as
// SIBLINGS of <Table> stay outside the scroll region. The standing risk is the
// other direction: a scroll container clips anything inside it that used to escape
// the table box. Audited across all ten consumers as of 2026-08-13 - no row cell
// renders an absolutely-positioned popover; WorkspacesPanel's ConfirmDialog is a
// sibling AND portals. If a future row needs a popover, it must portal.
//
// Enforced by web/src/components/holo/responsive.guard.test.ts, which fails if any
// <Table> call site omits minWidth.
//
// The base strings below contain ONLY utilities that are byte-identical across all
// eight consumers. Two competing Tailwind utilities on one element resolve by
// stylesheet order, not by class-attribute order, so a caller className cannot
// reliably override a base class: anything that varies is caller-supplied, never
// override-supplied. Class strings are literals so Tailwind v4's static scan emits
// them.
//
// Do NOT reach for this when a table does not need a shared grid template across
// its header and its rows - a native `<table>` is the right tool there (see
// web/src/workers/RevokedWorkersTable.tsx for the in-repo precedent).
const HEADER_BASE = 'border-b border-border font-mono text-[10px] text-fg-mute'
const ROW_BASE = 'items-center'

export type SortDirection = 'ascending' | 'descending' | 'none'

// One definition, replacing the four duplicated pairs in WorkersTable, UsersTable,
// EnrollmentsTable and ReservationsTable. `sort` is the wire-format sort value: the
// field name, optionally '-'-prefixed for descending. The '-' prefix is anchored to
// position 0 (not stripped from wherever the first hyphen happens to occur), so a
// field name containing its own interior hyphen is compared correctly - this is the
// behavior of the four helpers it replaces, kept byte-for-byte so the five
// already-roled tables are unchanged.
export function ariaSort(field: string, sort: string): SortDirection {
  const base = sort.startsWith('-') ? sort.slice(1) : sort
  if (base !== field) return 'none'
  return sort.startsWith('-') ? 'descending' : 'ascending'
}

export function sortCaret(field: string, sort: string): string {
  const base = sort.startsWith('-') ? sort.slice(1) : sort
  if (base !== field) return ''
  return sort.startsWith('-') ? ' ▼' : ' ▲'
}

export interface TableColumn<F extends string = string> {
  label: string
  // Present => sortable: renders a button, a caret and aria-sort.
  field?: F
  align?: 'right'
}

interface TableProps<F extends string> {
  // Required: becomes the accessible name of the table.
  label: string
  // The grid template class name only (Tailwind's bracketed arbitrary-value
  // syntax); Table prepends the base `grid` class. The string must stay in the
  // consumer file for Tailwind v4's static scan, but it is declared once there
  // instead of being applied to two elements by hand.
  columns: string
  // Optional Tailwind min-width utility (a literal, for Tailwind v4's static scan),
  // applied to the header row AND every TableRow, and the trigger for the
  // overflow-x-auto wrapper. Omit it only for a table with no fixed px tracks.
  minWidth?: string
  headers: TableColumn<F>[]
  sort?: string
  onSort?: (field: F) => void
  // The caller's header spacing and tracking deltas.
  headerClassName?: string
  children?: ReactNode
}

// The value is the grid class string - never a fresh object literal, so it stays
// referentially stable across renders. When minWidth is set the value is a template
// string, which is still stable in the sense React cares about: context change
// detection is Object.is, and Object.is compares equal strings as equal.
const ColumnsContext = createContext<string | null>(null)

export function Table<F extends string = string>({
  label,
  columns,
  minWidth,
  headers,
  sort = '',
  onSort,
  headerClassName,
  children,
}: TableProps<F>) {
  // ONE string for the header row and the body rows. The whole point of the context
  // is that these two cannot be put out of agreement by hand, and a min-width that
  // applied to only one of them would desynchronize the columns at exactly the
  // widths this exists to fix.
  const grid = minWidth ? `${columns} ${minWidth}` : columns
  const table = (
    <ColumnsContext.Provider value={grid}>
      <div role="table" aria-label={label}>
        <div role="row" className={`grid ${grid} ${HEADER_BASE} ${headerClassName ?? ''}`}>
          {headers.map((h, i) => {
            const field = h.field
            if (field === undefined) {
              // No aria-sort on a static header: it would advertise a sort
              // affordance that does not exist.
              return (
                <span key={i} role="columnheader" className={h.align === 'right' ? 'text-right' : undefined}>
                  {h.label}
                </span>
              )
            }
            // A header with `field` set but no `onSort` handler would otherwise
            // render a focusable, screen-reader-announced sort button that does
            // nothing when activated: it type-checks and looks correct while being
            // silently broken. Throw unconditionally, matching the ColumnsContext
            // guard below, so the omission surfaces in the first render instead of
            // shipping as a dead affordance.
            if (!onSort) throw new Error('Table: a header with `field` requires an `onSort` handler')
            return (
              <div
                key={i}
                role="columnheader"
                aria-sort={ariaSort(field, sort)}
                className={h.align === 'right' ? 'text-right' : undefined}
              >
                <button type="button" className="text-left" onClick={() => onSort(field)}>
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
  // Opt-in: with no minWidth the DOM is byte-identical to what eight consumers
  // shipped against, and a wrapper with no min-width would only scroll a misaligned
  // table anyway.
  if (!minWidth) return table
  return <div className="overflow-x-auto">{table}</div>
}

type TableRowProps = Omit<ComponentPropsWithoutRef<'div'>, 'role' | 'dangerouslySetInnerHTML'> & {
  as?: ElementType
  // Not part of HTMLAttributes<HTMLDivElement> (it belongs to button/input), but
  // TasksTable renders its selectable rows as `as="button" type="button"`.
  type?: 'button' | 'submit'
}

export function TableRow({ as, ...rest }: TableRowProps) {
  const columns = useContext(ColumnsContext)
  // A silent fallback would ship as a mangled layout in production. A throw is
  // unconditional, so it surfaces in the first test render instead.
  if (columns === null) throw new Error('TableRow must be rendered inside a Table')
  const Tag = as ?? 'div'
  const className = `grid ${columns} ${ROW_BASE} ${rest.className ?? ''}`
  // `rest` spreads BEFORE `role`: a caller-supplied `role` (accidental or via a
  // loosely-typed prop bag) must never win over the role this primitive owns.
  return <Tag {...rest} className={className} role="row" />
}

type TableCellProps = Omit<ComponentPropsWithoutRef<'span'>, 'role' | 'dangerouslySetInnerHTML'>

export function TableCell({ ...rest }: TableCellProps) {
  // Same ordering rule as TableRow: rest spreads before role, so a caller-supplied
  // role can never override the cell role this primitive owns.
  return <span {...rest} role="cell" />
}
