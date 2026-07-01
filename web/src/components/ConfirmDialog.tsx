import { useEffect, useId, useRef } from 'react'

interface ConfirmDialogProps {
  title: string
  body: string
  confirmLabel: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
}

// Minimal shared confirm primitive. No portal library, no focus-trap dependency:
// role="dialog" labelled by its title, Escape and Cancel both dismiss, and the
// cancel button is focused on open. Reused by Admin/Profile later.
export function ConfirmDialog({
  title,
  body,
  confirmLabel,
  destructive,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const titleId = useId()
  const cancelRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    cancelRef.current?.focus()
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onCancel])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="w-full max-w-sm rounded-card border border-border bg-bg p-5 shadow-xl"
      >
        <h2 id={titleId} className="text-[15px] font-medium text-fg">
          {title}
        </h2>
        <p className="mt-2 text-[13px] text-fg-mute">{body}</p>
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            ref={cancelRef}
            onClick={onCancel}
            className="rounded-md border border-border bg-white/5 px-3 py-1.5 text-[12px] text-fg-mute"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className={
              'rounded-md px-3 py-1.5 text-[12px] font-medium ' +
              (destructive ? 'bg-err/20 text-err border border-err/50' : 'bg-accent text-bg')
            }
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
