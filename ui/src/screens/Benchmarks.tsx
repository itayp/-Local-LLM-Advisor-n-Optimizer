import { Placeholder } from '../components/Placeholder'
import { en } from '../copy/en'

export function Benchmarks() {
  const c = en.screens.benchmarks
  return <Placeholder title={c.title} text={c.placeholder} step={c.step} />
}
