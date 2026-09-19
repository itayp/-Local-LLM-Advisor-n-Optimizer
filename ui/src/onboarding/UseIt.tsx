import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { ChatApp } from '../api/types'
import { en } from '../copy/en'

const c = en.onboarding.useit

/**
 * Screen 8: the exact model name with a copy button, then the chat apps
 * step 7's internal/chatapps package found on this machine — or, when it
 * found none, which ones exist and where to get them. The advisor never
 * installs a chat app itself (D-4); "Finish setup" is the one thing this
 * screen changes, and it says what it does.
 */
export function UseIt({ modelName, onFinish }: { modelName: string; onFinish: () => void }) {
  const [apps, setApps] = useState<ChatApp[] | null>(null)
  const [copied, setCopied] = useState(false)
  const [finishing, setFinishing] = useState(false)

  useEffect(() => {
    const ac = new AbortController()
    api
      .chatApps(ac.signal)
      .then((r) => setApps(r.apps))
      .catch(() => undefined)
    return () => ac.abort()
  }, [])

  const copy = () => {
    navigator.clipboard
      ?.writeText(modelName)
      .then(() => {
        setCopied(true)
        window.setTimeout(() => setCopied(false), 2000)
      })
      .catch(() => undefined)
  }

  const finish = () => {
    setFinishing(true)
    api
      .onboardingComplete()
      .catch(() => undefined) // the person is done from where they're standing either way
      .then(onFinish)
  }

  const found = (apps ?? []).filter((a) => a.found)
  const notFound = (apps ?? []).filter((a) => !a.found)

  return (
    <section className="onboarding__step" aria-labelledby="onboarding-title">
      <h1 id="onboarding-title">{c.title}</h1>
      <p className="screen__lead">{c.lead}</p>
      <p className="onboarding__model-name">
        <code data-testid="onboarding-model-name">{modelName}</code>{' '}
        <button type="button" className="link-button" onClick={copy}>
          {copied ? c.copied : c.copy}
        </button>
      </p>
      {apps === null ? (
        <p className="screen__note" role="status">
          {c.checkingApps}
        </p>
      ) : found.length > 0 ? (
        <>
          <p className="screen__note">{c.foundLead}</p>
          <ul className="notes">
            {found.map((a) => (
              <li key={a.id}>
                {a.name}
                {a.note ? <span className="facts__aside"> — {a.note}</span> : null}
              </li>
            ))}
          </ul>
        </>
      ) : (
        <>
          <p className="screen__note">{c.noneFoundLead}</p>
          <ul className="notes">
            {notFound.map((a) => (
              <li key={a.id}>
                <a href={a.download_url} target="_blank" rel="noreferrer">
                  {a.name}
                </a>
                {a.note ? <span className="facts__aside"> — {a.note}</span> : null}
              </li>
            ))}
          </ul>
        </>
      )}
      <button type="button" className="button" disabled={finishing} onClick={finish}>
        {finishing ? c.finishing : c.finish}
      </button>
    </section>
  )
}
