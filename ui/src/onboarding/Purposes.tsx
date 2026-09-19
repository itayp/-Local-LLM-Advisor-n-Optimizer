import type { Purpose } from '../api/types'
import { en } from '../copy/en'

const c = en.onboarding.purposes

/** The order the checkboxes are offered in: the beginner's default first — matches Recommend.tsx's own ordering. */
const order: Purpose[] = ['chat', 'writing', 'coding', 'reasoning', 'long_context', 'vision', 'agentic']

/** Screen 4: the purposes enum as checkboxes, "Not sure — general chat" checked by default. */
export function Purposes({
  purposes,
  onChange,
  onNext,
}: {
  purposes: Purpose[]
  onChange: (p: Purpose[]) => void
  onNext: () => void
}) {
  const toggle = (p: Purpose) => onChange(purposes.includes(p) ? purposes.filter((x) => x !== p) : [...purposes, p])
  return (
    <section className="onboarding__step" aria-labelledby="onboarding-title">
      <h1 id="onboarding-title">{c.title}</h1>
      <fieldset className="purposes">
        <legend className="screen__lead">{c.help}</legend>
        <ul>
          {order.map((p) => (
            <li key={p}>
              <label>
                <input type="checkbox" checked={purposes.includes(p)} onChange={() => toggle(p)} /> {c.labels[p]}
              </label>
            </li>
          ))}
        </ul>
      </fieldset>
      <button type="button" className="button" onClick={onNext}>
        {c.continue}
      </button>
    </section>
  )
}
