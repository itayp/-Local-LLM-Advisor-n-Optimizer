import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { App } from './App'
import './index.css'
import { OnboardingGate } from './onboarding/OnboardingGate'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <OnboardingGate>
        <App />
      </OnboardingGate>
    </BrowserRouter>
  </StrictMode>,
)
