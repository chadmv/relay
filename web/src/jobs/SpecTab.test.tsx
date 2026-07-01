import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'
import { SpecTab } from './SpecTab'
import type { TaskDetail } from './api'

const task: TaskDetail = {
  id: 't1', name: 'frame-001', status: 'done',
  commands: [['blender', '-b', 'scene.blend'], ['echo', 'done']],
  env: { CUDA: '1' },
  requires: { gpu: 'true' },
  timeout_seconds: 3600, retries: 2, retry_count: 0,
}

test('renders each command line', () => {
  render(<SpecTab task={task} />)
  expect(screen.getByText(/blender -b scene\.blend/)).toBeInTheDocument()
  expect(screen.getByText(/echo done/)).toBeInTheDocument()
})

test('renders env and requires entries', () => {
  render(<SpecTab task={task} />)
  expect(screen.getByText(/CUDA/)).toBeInTheDocument()
  expect(screen.getByText(/gpu/)).toBeInTheDocument()
})

test('renders a placeholder when no task is selected', () => {
  render(<SpecTab task={undefined} />)
  expect(screen.getByText(/select a task/i)).toBeInTheDocument()
})
