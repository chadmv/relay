import type { ButtonHTMLAttributes } from 'react'

export function Button(props: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...props}
      className={
        'w-full rounded-[8px] bg-accent px-3 py-2 text-[13px] font-medium text-bg ' +
        'transition hover:bg-accent-b disabled:opacity-50 ' +
        (props.className ?? '')
      }
    />
  )
}
