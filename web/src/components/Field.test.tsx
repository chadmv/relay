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
