import { Placeholder } from '../components/Placeholder'
import { en } from '../copy/en'

export function Recommend() {
  const c = en.screens.recommend
  return <Placeholder title={c.title} text={c.placeholder} step={c.step} />
}
