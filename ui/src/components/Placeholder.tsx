import { en } from '../copy/en'
import { useAdvanced } from '../state/settings'

/**
 * Placeholder is what every screen is in step 1: its title, one sentence of
 * what it will show, and which build-plan step fills it in. It also shows
 * the Advanced toggle's effect, so the wiring is visible before any screen
 * has advanced content.
 */
export function Placeholder({ title, text, step }: { title: string; text: string; step: string }) {
  const advanced = useAdvanced()
  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{title}</h1>
      <p className="screen__lead">{text}</p>
      <p className="screen__note">{en.placeholder.comingIn(step)}</p>
      {advanced ? (
        <p className="screen__advanced" data-testid="advanced-note">
          {en.app.advancedOn}: technical columns will appear here.
        </p>
      ) : null}
    </section>
  )
}
