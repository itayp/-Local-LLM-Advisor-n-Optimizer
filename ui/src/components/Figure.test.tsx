import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Figure, formatBytes, formatRate } from './Figure'

describe('Figure (product rule 4)', () => {
  it('renders an estimate and a measurement with different treatments', () => {
    render(
      <>
        <Figure bytes={{ value: 15_890_000_000, source: 'estimated' }} />
        <Figure bytes={{ value: 15_890_000_000, source: 'measured' }} />
      </>,
    )
    const [est, meas] = screen.getAllByText(/GB/)
    expect(est).toHaveAttribute('data-source', 'estimated')
    expect(est).toHaveClass('figure--estimated')
    expect(est).toHaveTextContent('≈ 14.8 GB')
    expect(meas).toHaveAttribute('data-source', 'measured')
    expect(meas).toHaveClass('figure--measured')
    expect(meas).toHaveTextContent('14.8 GB')
    expect(meas).not.toHaveTextContent('≈')
    expect(est.className).not.toEqual(meas.className)
  })

  it('shows an estimated rate as a range and a measured one as a point', () => {
    render(
      <>
        <Figure rate={{ value: 50, low: 45, high: 55, unit: 'tok/s', source: 'estimated' }} />
        <Figure rate={{ value: 51.2, low: 51.2, high: 51.2, unit: 'tok/s', source: 'measured' }} />
      </>,
    )
    expect(screen.getByText(/45\.0–55\.0 tok\/s/)).toHaveAttribute('data-source', 'estimated')
    expect(screen.getByText('51.2 tok/s')).toHaveAttribute('data-source', 'measured')
  })

  it('formats sizes and rates the way a person reads them', () => {
    expect(formatBytes(4_900_000_000)).toBe('4.6 GB')
    expect(formatBytes(16 * 1024 ** 3)).toBe('16 GB')
    expect(formatBytes(250 * 1024 ** 2)).toBe('250 MB')
    expect(formatRate({ value: 120, low: 120, high: 120, unit: 'tok/s', source: 'measured' })).toBe('120 tok/s')
  })
})
