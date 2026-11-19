import type { ReactNode } from 'react'
import './AppShell.css'

export type AppPage = 'monitor' | 'activity' | 'publishers' | 'settings'
type NavigationProps = { publisherAttention?: number; active: AppPage; onMonitor?: () => void; onActivity?: () => void; onPublishers?: () => void; onAccounts?: () => void; disabled?: boolean }
type Props = NavigationProps & { user?: { username: string }; onLogout?: () => void; connection?: ReactNode; sessionError?: string; children: ReactNode }

function NavigationIcon({ page }: { page: AppPage }) {
  if (page === 'settings') return <svg className="sidebar-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m9 3-.6 2.4-1.6.9-2.4-.6-3 5.2 1.8 1.7v1.8L1.4 16l3 5.2 2.4-.6 1.6.9L9 24h6l.6-2.5 1.6-.9 2.4.6 3-5.2-1.8-1.6v-1.8l1.8-1.7-3-5.2-2.4.6-1.6-.9L15 3Z" transform="translate(2 0) scale(.83)" /><circle cx="12" cy="11.2" r="3.2" /></svg>
  return <svg className="sidebar-icon" viewBox="0 0 24 24" aria-hidden="true">{page === 'monitor' ? <><rect x="3" y="4" width="18" height="13" rx="2" /><path d="M8 21h8M12 17v4M6 11h3l2-4 3 7 2-3h2" /></> : page === 'activity' ? <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></> : page === 'publishers' ? <><rect x="3" y="13" width="6" height="7" rx="1" /><rect x="15" y="13" width="6" height="7" rx="1" /><path d="M6 13V8h12v5M12 8V3m-3 3 3-3 3 3" /></> : <><circle cx="9" cy="7" r="3" /><path d="M3 21v-3a6 6 0 0 1 12 0v3M16 4a3 3 0 0 1 0 6m2 4a5 5 0 0 1 3 4v3" /></>}</svg>
}

const destinations = [['monitor', 'Monitor'], ['activity', 'Activity'], ['publishers', 'Publishers'], ['settings', 'Settings']] as const

export function AppNavigation({ publisherAttention = 0, active, onMonitor, onActivity, onPublishers, onAccounts, disabled = false, mobile = false }: NavigationProps & { mobile?: boolean }) {
  const actions = { monitor: onMonitor, activity: onActivity, publishers: onPublishers, settings: onAccounts }
  const items = mobile ? destinations : destinations.slice(0, 3)
  return <nav className={mobile ? 'mobile-navigation' : 'app-navigation'} aria-label={mobile ? 'Mobile navigation' : 'Main navigation'}>{items.map(([page, label]) => <a key={page} href={`#${page}`} aria-label={label} title={label} aria-current={active === page ? 'page' : undefined} aria-disabled={disabled || undefined} onClick={event => {
    const action = actions[page]
    if (disabled || action) event.preventDefault()
    if (!disabled) action?.()
  }}><NavigationIcon page={page} /><span className="sidebar-label">{label}</span>{page === 'publishers' && publisherAttention > 0 && <span className="publisher-attention-count" aria-label={`${publisherAttention} publishers need attention`}>{publisherAttention}</span>}{!mobile && <span className="sidebar-arrow" aria-hidden="true">›</span>}</a>)}</nav>
}

export function AppShell({ publisherAttention, active, user, onMonitor, onActivity, onPublishers, onAccounts, onLogout, connection, sessionError, disabled = false, children }: Props) {
  return <div className="app-shell">
    <a className="skip-link" href="#page-content" onClick={event => { event.preventDefault(); document.getElementById('page-content')?.focus() }}>Skip to page content</a>
    <aside className="app-sidebar" aria-label="Workspace">
      <a className="brand" href="#monitor" aria-label="Services home" aria-disabled={disabled || undefined} onClick={event => { if (disabled || onMonitor) event.preventDefault(); if (!disabled) onMonitor?.() }}>
        <span className="brand-mark" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none"><path d="M3 12h4l3-7 4 14 3-7h4" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg></span><span className="sidebar-brand-name">services</span>
      </a>

      <AppNavigation publisherAttention={publisherAttention} active={active} onMonitor={onMonitor} onActivity={onActivity} onPublishers={onPublishers} onAccounts={onAccounts} disabled={disabled} />
      <div className="sidebar-footer">
        {connection && <div className="sidebar-connection">{connection}</div>}
        {user && <div className="sidebar-profile" title={`${user.username} · Administrator`}><span className="sidebar-avatar" aria-hidden="true">{Array.from(user.username)[0]?.toLocaleUpperCase()}</span><div className="sidebar-user-details"><strong className="sidebar-user">{user.username}</strong><span>Administrator</span></div><a className="sidebar-settings" href="#settings" aria-label="Settings" title="Settings" aria-current={active === 'settings' ? 'page' : undefined} aria-disabled={disabled || undefined} onClick={event => {
          if (disabled || onAccounts) event.preventDefault()
          if (!disabled) onAccounts?.()
        }}><svg className="sidebar-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m9 3-.6 2.4-1.6.9-2.4-.6-3 5.2 1.8 1.7v1.8L1.4 16l3 5.2 2.4-.6 1.6.9L9 24h6l.6-2.5 1.6-.9 2.4.6 3-5.2-1.8-1.6v-1.8l1.8-1.7-3-5.2-2.4.6-1.6-.9L15 3Z" transform="translate(2 0) scale(.83)" /><circle cx="12" cy="11.2" r="3.2" /></svg></a></div>}
        {onLogout && <button type="button" className="sidebar-signout" aria-label="Sign out" title="Sign out" onClick={onLogout} disabled={disabled}><svg className="sidebar-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M9 5H5v14h4M13 8l4 4-4 4M8 12h13" /></svg><span className="sidebar-label">Sign out</span></button>}
      </div>
    </aside>
    <div className="app-content" id="page-content" tabIndex={-1}>{sessionError && <p role="alert" className="notice danger session-notice">{sessionError}</p>}{children}</div>
    <AppNavigation publisherAttention={publisherAttention} active={active} onMonitor={onMonitor} onActivity={onActivity} onPublishers={onPublishers} onAccounts={onAccounts} disabled={disabled} mobile />
  </div>
}
