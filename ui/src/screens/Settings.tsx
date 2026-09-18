import { en } from '../copy/en'
import { useSettings } from '../state/settings'

export function Settings() {
  const c = en.screens.settings
  const { settings, setAdvanced } = useSettings()
  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>
      <p className="screen__lead">{c.placeholder}</p>
      <p className="screen__note">{en.placeholder.comingIn(c.step)}</p>

      <div className="setting">
        <label className="setting__label">
          <input
            type="checkbox"
            checked={settings.advanced}
            onChange={(e) => setAdvanced(e.target.checked)}
            aria-describedby="advanced-help"
          />{' '}
          {c.advancedLabel}
        </label>
        <p id="advanced-help" className="setting__help">
          {c.advancedHelp}
        </p>
      </div>
    </section>
  )
}
