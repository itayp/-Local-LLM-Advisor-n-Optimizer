import { formatDownload } from './format'

/**
 * Progress is the one shared "here's what's happening and how long it
 * usually takes" strip the onboarding flow uses for both an Ollama
 * install and a model pull (CLAUDE.md: "Every wait shows what's
 * happening"). completed/total are bytes read live from the download's
 * own counter — facts, never a <Figure>.
 */
export function Progress({ label, completed, total }: { label: string; completed: number; total?: number }) {
  return (
    <div className="onboarding__progress" role="status">
      <p>{label}</p>
      {total ? (
        <>
          <progress value={completed} max={total} />
          <p className="screen__note">
            {formatDownload(completed)} / {formatDownload(total)}
          </p>
        </>
      ) : (
        <p className="screen__note">{formatDownload(completed)}</p>
      )}
    </div>
  )
}
