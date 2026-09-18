import { Placeholder } from '../components/Placeholder'
import { en } from '../copy/en'

export function Models() {
  const c = en.screens.models
  return <Placeholder title={c.title} text={c.placeholder} step={c.step} />
}
