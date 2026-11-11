import type { ReactNode } from 'react'
import './PageHeader.css'

type Props = { title: string; actions?: ReactNode; id?: string }

export function PageHeader({ title, actions, id }: Props) {
  return <header className="page-heading unified-page-header">
    <h1 id={id}>{title}</h1>
    {actions && <div className="heading-actions">{actions}</div>}
  </header>
}
