import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { Field } from './Field'
import { Input } from './Input'

test('associates the label with its control and shows error text', () => {
  render(
    <Field label="Email" htmlFor="email" error="required">
      <Input id="email" />
    </Field>,
  )
  expect(screen.getByLabelText('Email')).toBeInTheDocument()
  expect(screen.getByText('required')).toBeInTheDocument()
})

test('an error is announced: role="alert" and wired to the control via aria-describedby', () => {
  render(
    <Field label="Email" htmlFor="email" error="required">
      <Input id="email" />
    </Field>,
  )
  const error = screen.getByRole('alert')
  expect(error).toHaveTextContent('required')
  const input = screen.getByLabelText('Email')
  expect(input).toHaveAttribute('aria-describedby', error.id)
})

test('no error means no alert role and no aria-describedby on the control', () => {
  render(
    <Field label="Email" htmlFor="email">
      <Input id="email" />
    </Field>,
  )
  expect(screen.queryByRole('alert')).toBeNull()
  expect(screen.getByLabelText('Email')).not.toHaveAttribute('aria-describedby')
})
