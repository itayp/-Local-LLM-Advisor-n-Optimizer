import type { ComponentType } from 'react'
import { en } from '../copy/en'
import { Benchmarks } from './Benchmarks'
import { Computer } from './Computer'
import { Home } from './Home'
import { Models } from './Models'
import { Ollama } from './Ollama'
import { Recommend } from './Recommend'
import { Settings } from './Settings'
import { Watch } from './Watch'

/**
 * The screens, in navigation order — the MVP workflow of PRD §17:
 *
 *   detect hardware → detect Ollama → analyse installed models →
 *   recommend → benchmark and store results → monitor new models → notify
 *
 * Both the navigation and the routes are generated from this list, so a
 * screen exists in exactly one place. Step 7 adds the first-run onboarding
 * flow in front of it; step 8 fills in Home, Models and Settings (Recommend
 * and Benchmarks were already built, in steps 5 and 6, to give those
 * steps' own gates something to run on a machine without a terminal).
 * Ollama and New models stay placeholders — the first was never assigned
 * its own screen, the second is step 10's.
 */
export interface Screen {
  path: string
  label: string
  Component: ComponentType
}

export const screens: readonly Screen[] = [
  { path: '/', label: en.nav.home, Component: Home },
  { path: '/computer', label: en.nav.computer, Component: Computer },
  { path: '/ollama', label: en.nav.ollama, Component: Ollama },
  { path: '/models', label: en.nav.models, Component: Models },
  { path: '/recommend', label: en.nav.recommend, Component: Recommend },
  { path: '/benchmarks', label: en.nav.benchmarks, Component: Benchmarks },
  { path: '/watch', label: en.nav.watch, Component: Watch },
  { path: '/settings', label: en.nav.settings, Component: Settings },
]
