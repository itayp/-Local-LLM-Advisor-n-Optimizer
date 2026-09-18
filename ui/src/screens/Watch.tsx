import { Placeholder } from '../components/Placeholder'
import { en } from '../copy/en'

export function Watch() {
  const c = en.screens.watch
  return <Placeholder title={c.title} text={c.placeholder} step={c.step} />
}
