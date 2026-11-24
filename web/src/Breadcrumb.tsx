import { serviceHref } from './navigation.ts'
import './Breadcrumb.css'

type Props = { parentProject?: string }

export function Breadcrumb({ parentProject }: Props) {
  return <nav className="breadcrumb" aria-label="Breadcrumb"><ol><li><a href="#monitor">Monitor</a></li>{parentProject !== undefined && <li><span className="breadcrumb-separator" aria-hidden="true">/</span><a href={serviceHref(parentProject)}>{parentProject}</a></li>}</ol></nav>
}
