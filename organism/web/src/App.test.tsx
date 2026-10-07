import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import App from './App'

describe('App', () => {
  it('points the owner to the control plane', () => {
    render(<App />)
    expect(screen.getByRole('link', { name: '/_seed' })).toHaveAttribute('href', '/_seed/')
  })
})
