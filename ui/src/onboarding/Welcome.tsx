import { en } from '../copy/en'

const c = en.onboarding.welcome

/** Screen 1 of the first-run flow: what this does, and that it does not chat. One button. */
export function Welcome({ onNext }: { onNext: () => void }) {
  return (
    <section className="onboarding__step" aria-labelledby="onboarding-title">
      <h1 id="onboarding-title">{c.title}</h1>
      <p className="screen__lead">{c.what}</p>
      <p className="screen__lead">{c.notChat}</p>
      <button type="button" className="button" onClick={onNext}>
        {c.start}
      </button>
    </section>
  )
}
