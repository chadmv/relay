import { useId, useRef } from 'react'
import { DialogShell } from './dialog/DialogShell'

interface ConfirmDialogProps {
  title: string
  body: string
  confirmLabel: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
}

// Minimal shared confirm primitive, used at five call sites. The modal behavior -
// portal, focus trap, inert background, scroll lock, scoped Escape, focus restore
// - lives in DialogShell; this file owns only the copy and the two buttons. The
// public props are unchanged, so no call site moved.
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

  return (
    <DialogShell
      titleId={titleId}
      onDismiss={onCancel}
      initialFocusRef={cancelRef}
      panelClassName="max-w-sm"
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
    </DialogShell>
  )
}
