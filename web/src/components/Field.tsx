import { cloneElement, isValidElement, type ReactElement, type ReactNode } from 'react'

interface FieldProps {
  label: string
  htmlFor: string
  error?: string
  hint?: ReactNode
  children: ReactNode
}

// Accessible error wiring: the error text carries role="alert" so a screen
// reader announces it the moment it appears (matches the ARIA precedent set
// for the workers table), and its id is pushed onto the control's
// aria-describedby so the association survives even when the control is not
// currently focused. The control is cloned rather than asking every caller to
// forward a describedby prop by hand - Field owns the only place that knows
// both the error id and the control, and every call site here passes exactly
// one <Input> as children.
export function Field({ label, htmlFor, error, hint, children }: FieldProps) {
  const errorId = `${htmlFor}-error`
  const control =
    error && isValidElement(children)
      ? cloneElement(children as ReactElement<{ 'aria-describedby'?: string }>, {
          'aria-describedby': errorId,
        })
      : children

  return (
    <div className="mb-3">
      <label
        htmlFor={htmlFor}
        className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute"
      >
        {label}
      </label>
      {control}
      {hint && <div className="mt-1 text-[11px] text-fg-dim">{hint}</div>}
      {error && (
        <div id={errorId} role="alert" className="mt-1 text-[11px] text-err">
          {error}
        </div>
      )}
    </div>
  )
}
