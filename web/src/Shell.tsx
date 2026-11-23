import { useEffect, useState } from 'react'
import { postJSON, type AuthInfo } from './auth.ts'
import { History } from './History.tsx'
import { Queue } from './Queue.tsx'
import { Resources } from './resources/Resources.tsx'
import { Password } from './Password.tsx'
import { Settings } from './Settings.tsx'
import { useLiveStatus } from './status.ts'

const pages = [
  { path: '/', label: 'Approval Queue' },
  { path: '/history', label: 'History' },
  { path: '/resources', label: 'Resources' },
  { path: '/settings', label: 'Settings' },
  { path: '/password', label: 'Password update' },
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

function approvalID(path: string): number | null {
  const prefix = '/approval/'
  if (!path.startsWith(prefix)) {
    return null
  }
  const raw = path.slice(prefix.length)
  if (!/^[1-9]\d*$/.test(raw)) {
    return null
  }
  return Number(raw)
}

export function Shell({ auth, onChange }: { auth: AuthInfo; onChange: () => void }) {
  const { path, go } = usePath()
  const { status, queueTick } = useLiveStatus(onChange)
  const pendingCount = usePendingCount(queueTick)
  const focusID = approvalID(path)
  const page = focusID != null ? pages[0] : pages.find((item) => item.path === path)

  async function lock() {
    await postJSON('/api/lock', {})
    onChange()
  }

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">GateKeep<BuildVersion /></div>
        <nav className="nav" aria-label="Application">
          {pages.map((item) => (
            <button
              key={item.path}
              type="button"
              className="nav-button"
              aria-current={item.path === path || (item.path === '/' && focusID != null) ? 'page' : undefined}
              onClick={() => go(item.path)}
            >
              <span>{item.label}</span>
              {item.path === '/' && pendingCount > 0 ? (
                <span className="nav-count" aria-label={`${pendingCount} pending`}>
                  {pendingCount}
                </span>
              ) : null}
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
        ) : path === '/password' ? (
          <Password />
        ) : path === '/resources' ? (
          <Resources />
        ) : path === '/' || focusID != null ? (
          <Queue focusID={focusID} tick={queueTick} />
        ) : path === '/history' ? (
          <History tick={queueTick} />
        ) : (
          <p>That page does not exist.</p>
        )}
      </main>
    </div>
  )
}

function usePendingCount(tick: number) {
  const [count, setCount] = useState(0)

  useEffect(() => {
    let stop = false
    fetch('/api/approvals')
      .then(async (response) => {
        if (!response.ok || stop) {
          return
        }
        const calls = (await response.json()) as { status: string }[]
        setCount(calls.filter((call) => call.status === 'pending' || call.status === 'executing' || call.status === 'review').length)
      })
      .catch(() => {})
    return () => {
      stop = true
    }
  }, [tick])

  return count
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

function BuildVersion() {
  const [build, setBuild] = useState<{ version: string; commit: string } | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    fetch('/api/version', { signal: controller.signal, cache: 'no-store' })
      .then(async (response) => {
        if (response.ok) setBuild(await response.json())
      })
      .catch(() => {})
    return () => controller.abort()
  }, [])

  return build ? <small className="build-version" title={`Commit ${build.commit}`}>{build.version}</small> : null
}
