import { useState } from 'react'
import type { Purpose, Rate, Recommendation } from '../api/types'
import { en } from '../copy/en'
import { Checking } from './Checking'
import { Getting } from './Getting'
import { OllamaStep } from './OllamaStep'
import { Purposes } from './Purposes'
import { Recommendations } from './Recommendations'
import { TryIt } from './TryIt'
import { UseIt } from './UseIt'
import { Welcome } from './Welcome'

type Step = 'welcome' | 'checking' | 'ollama' | 'purposes' | 'recommend' | 'getting' | 'tryit' | 'useit'

const steps: Step[] = ['welcome', 'checking', 'ollama', 'purposes', 'recommend', 'getting', 'tryit', 'useit']

/**
 * Onboarding (build-plan step 7): one screen per state, in PRD §17's
 * workflow order — welcome, hardware, Ollama, purposes, recommendations,
 * getting one, trying it, using it. It owns the little state one step
 * hands the next (which model was picked, its estimate, the name that
 * ends up on this computer) so nothing downstream re-asks for it.
 *
 * main.tsx renders this instead of <App> until GET /api/onboarding says
 * completed (OnboardingGate) — deliberately outside App's own route
 * tree, so App, its tests, and every screen's own tests are untouched.
 */
export function Onboarding({ onFinished }: { onFinished: () => void }) {
  const [step, setStep] = useState<Step>('welcome')
  const [purposes, setPurposes] = useState<Purpose[]>(['chat'])
  const [chosen, setChosen] = useState<Recommendation | null>(null)
  const [priorEstimate, setPriorEstimate] = useState<Rate | undefined>(undefined)
  const [modelName, setModelName] = useState('')

  const download = (r: Recommendation) => {
    setChosen(r)
    setPriorEstimate(r.speed)
    if (r.installed) {
      setModelName(r.pull_name)
      setStep('tryit')
    } else {
      setStep('getting')
    }
  }

  return (
    <div className="onboarding">
      <ol className="onboarding__dots" aria-label={en.onboarding.progress}>
        {steps.map((s, i) => (
          <li key={s} className={s === step ? 'active' : i < steps.indexOf(step) ? 'done' : undefined} />
        ))}
      </ol>
      <div className="onboarding__body">
        {step === 'welcome' && <Welcome onNext={() => setStep('checking')} />}
        {step === 'checking' && <Checking onNext={() => setStep('ollama')} />}
        {step === 'ollama' && <OllamaStep onNext={() => setStep('purposes')} />}
        {step === 'purposes' && <Purposes purposes={purposes} onChange={setPurposes} onNext={() => setStep('recommend')} />}
        {step === 'recommend' && <Recommendations purposes={purposes} onDownload={download} />}
        {step === 'getting' && chosen && (
          <Getting
            recommendation={chosen}
            onDone={() => {
              setModelName(chosen.pull_name)
              setStep('tryit')
            }}
          />
        )}
        {step === 'tryit' && <TryIt modelName={modelName} priorEstimate={priorEstimate} onNext={() => setStep('useit')} />}
        {step === 'useit' && <UseIt modelName={modelName} onFinish={onFinished} />}
      </div>
    </div>
  )
}
