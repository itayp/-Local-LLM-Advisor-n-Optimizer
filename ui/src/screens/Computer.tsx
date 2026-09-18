import { Placeholder } from '../components/Placeholder'
import { en } from '../copy/en'

export function Computer() {
  const c = en.screens.computer
  return <Placeholder title={c.title} text={c.placeholder} step={c.step} />
}
