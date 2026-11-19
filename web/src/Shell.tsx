import { useEffect, useState } from 'react'
import { postJSON, type AuthInfo } from './auth.ts'
import { Settings } from './Settings.tsx'
import { useLiveStatus } from './status.ts'

const pages = [
  { path: '/', label: 'Approval Queue' },
  { path: '/resources', label: 'Resources' },
  { path: '/history', label: 'History' },
  { path: '/settings', label: 'Settings' },
]

function usePath() {
  const [path, setPath] = useState(window.location.pathname)

  useEffect(() => {
    const onPop = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  function go(next: string) {
    if (next === window.location.pathname) {
      return
    }
    window.history.pushState(null, '', next)
    setPath(next)
  }

  return { path, go }
}

export function Shell({ auth, onChange }: { auth: AuthInfo; onChange: () => void }) {
  const { path, go } = usePath()
  const status = useLiveStatus(onChange)
  const page = pages.find((item) => item.path === path)

  async function lock() {
    await postJSON('/api/lock', {})
    onChange()
  }

  return (
    <div className="shell">
      <aside className="sidebar">
        <p className="brand">GateKeep</p>
        <nav className="nav" aria-label="Application">
          {pages.map((item) => (
            <button
              key={item.path}
              type="button"
              className="nav-button"
              aria-current={item.path === path ? 'page' : undefined}
              onClick={() => go(item.path)}
            >
              {item.label}
            </button>
          ))}
        </nav>
        <div className="status" aria-live="polite">
          <StatusRow label="Server" value={status.server} />
          <StatusRow label="Database" value={status.database} />
          <button type="button" className="nav-button" onClick={lock}>
            Lock
          </button>
        </div>
      </aside>
      <main>
        <h1>{page ? page.label : 'Not found'}</h1>
        {path === '/settings' ? (
          <Settings auth={auth} onChange={onChange} />
        ) : page ? (
          <p>Nothing here yet.</p>
        ) : (
          <p>That page does not exist.</p>
        )}
      </main>
    </div>
  )
}

function StatusRow({ label, value }: { label: string; value: string }) {
  return (
    <p className="status-row">
      <span className={`dot dot-${value}`} aria-hidden="true" />
      <span>{label}</span>
      <span className="status-value">{value}</span>
    </p>
  )
}
