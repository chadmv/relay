import type { ReactNode } from 'react'

interface FieldProps {
  label: string
  htmlFor: string
  error?: string
  hint?: ReactNode
  children: ReactNode
}

export function Field({ label, htmlFor, error, hint, children }: FieldProps) {
  return (
    <div className="mb-3">
      <label
        htmlFor={htmlFor}
        className="mb-1 block font-mono text-[10px] uppercase tracking-[0.16em] text-fg-mute"
      >
        {label}
      </label>
      {children}
      {hint && <div className="mt-1 text-[11px] text-fg-dim">{hint}</div>}
      {error && <div className="mt-1 text-[11px] text-err">{error}</div>}
    </div>
  )
}
