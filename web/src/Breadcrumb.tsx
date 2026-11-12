import { serviceHref } from './navigation.ts'
import './Breadcrumb.css'

type Props = { project?: string; container?: string; current?: string; root?: string }

export function Breadcrumb({ project, container, current, root = 'Monitor' }: Props) {
  const items: { label: string; href?: string }[] = [
    { label: root, ...(project || current ? { href: '#monitor' } : {}) },
    ...(project ? [{ label: project, ...(container || current ? { href: serviceHref(project) } : {}) }] : []),
    ...(container ? [{ label: container }] : []),
    ...(current ? [{ label: current }] : []),
  ]
  return <nav className="breadcrumb" aria-label="Breadcrumb"><ol>{items.map((item, index) => <li key={index}>{index > 0 && <span className="breadcrumb-separator" aria-hidden="true">/</span>}{item.href ? <a href={item.href}>{item.label}</a> : <span aria-current="page">{item.label}</span>}</li>)}</ol></nav>
}
