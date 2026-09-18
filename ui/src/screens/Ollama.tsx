import { Placeholder } from '../components/Placeholder'
import { en } from '../copy/en'

export function Ollama() {
  const c = en.screens.ollama
  return <Placeholder title={c.title} text={c.placeholder} step={c.step} />
}
