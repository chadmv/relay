import type { InputHTMLAttributes } from 'react'

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className={
        'w-full rounded-[8px] border border-border bg-white/5 px-3 py-2 text-[13px] ' +
        'text-fg placeholder:text-fg-dim outline-none focus:border-accent ' +
        (props.className ?? '')
      }
    />
  )
}
