import { Placeholder } from '../components/Placeholder'
import { en } from '../copy/en'

export function Home() {
  const c = en.screens.home
  return <Placeholder title={c.title} text={c.placeholder} step={c.step} />
}
