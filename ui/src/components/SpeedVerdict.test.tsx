import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { SpeedVerdict as Verdict } from '../api/types'
import { en } from '../copy/en'
import { SpeedVerdict, SpeedWithVerdict } from './SpeedVerdict'

const est: Verdict = { purpose: 'chat', known: true, low: 'good', high: 'excellent', limit: 'answer_speed', source: 'estimated', text: 'good to excellent for everyday chat' }
const meas: Verdict = {
  purpose: 'coding', known: true, low: 'usable', high: 'usable', limit: 'wait', source: 'measured', text: 'usable for coding',
  wait_text: 'about 5 seconds to read a pasted file',
}
const unmeasured: Verdict = {
  purpose: 'long_context', known: true, low: 'excellent', high: 'excellent', limit: 'answer_speed', source: 'measured',
  text: 'excellent for long documents', note: 'Graded on answer speed alone: the test has no prompt as long as a long document.',
}

describe('SpeedVerdict (product rule 4)', () => {
  it('shows an estimated and a measured verdict with the two figure treatments, a line each', () => {
    render(<SpeedVerdict verdicts={[est, meas]} />)
    const [m, e] = screen.getAllByTestId('speed-verdict')
    expect(m).toHaveAttribute('data-source', 'measured')
    expect(m).toHaveTextContent(`${en.verdict.measured}: usable for coding — about 5 seconds to read a pasted file`)
    expect(m.querySelector('.verdict__chip')).toHaveClass('figure--measured')
    expect(e).toHaveAttribute('data-source', 'estimated')
    expect(e).toHaveTextContent(`${en.verdict.estimated}: good to excellent for everyday chat`)
    expect(e.querySelector('.verdict__chip')).toHaveClass('figure--estimated')
    expect(m.className).not.toEqual(e.className)
  })

  it('says in words what a verdict rests on, except in the compact chip', () => {
    const { unmount } = render(<SpeedVerdict verdicts={[unmeasured]} />)
    expect(screen.getByText(unmeasured.note!)).toBeInTheDocument()
    unmount()
    render(<SpeedVerdict verdicts={[unmeasured]} compact />)
    expect(screen.queryByText(unmeasured.note!)).not.toBeInTheDocument()
  })

  it('opens the speed explainer, which says the bars are provisional', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(
          JSON.stringify({ words_per_token: 0.75, purposes: [{ purpose: 'chat', mode: 'read_along', stream: { excellent: 10, good: 5.29, usable: 3.56 }, wait_s: { excellent: 1, good: 4, usable: 10 } }] }),
          { status: 200 },
        ),
      ),
    )
    render(<SpeedVerdict verdicts={[meas]} />)
    const line = screen.getByTestId('speed-verdict')
    await userEvent.click(within(line).getByRole('button', { name: 'What is tok/s?' }))
    expect(await within(line).findByTestId('speed-needs-provisional')).toHaveTextContent(en.speedNeeds.provisional)
  })

  it('renders nothing without verdicts, and the speed keeps its explainer', () => {
    const { container } = render(<SpeedVerdict verdicts={[]} />)
    expect(container).toBeEmptyDOMElement()
    render(<SpeedWithVerdict rate={{ value: 41.3, low: 41.3, high: 41.3, unit: 'tok/s', source: 'measured' }} />)
    expect(screen.getByText('41.3 tok/s')).toHaveAttribute('data-source', 'measured')
    expect(screen.getByRole('button', { name: 'What is tok/s?' })).toBeInTheDocument()
  })
})
