import { useCallback, useEffect, useState } from 'react'
import { getAuth, type AuthInfo } from './auth.ts'
import { Gate, Setup, Unlock } from './Gate.tsx'
import { Shell } from './Shell.tsx'

export default function App() {
  const [auth, setAuth] = useState<AuthInfo | null>(null)
  const [failed, setFailed] = useState(false)

  const reload = useCallback(() => {
    getAuth()
      .then((next) => {
        setAuth(next)
        setFailed(false)
      })
      .catch(() => setFailed(true))
  }, [])

  useEffect(() => {
    reload()
  }, [reload])

  if (failed) {
    return (
      <Gate title="GateKeep">
        <p>Could not reach the application.</p>
      </Gate>
    )
  }
  if (!auth) {
    return (
      <Gate title="GateKeep">
        <p>Loading…</p>
      </Gate>
    )
  }
  if (auth.state === 'setup') {
    return <Setup onUnlocked={reload} />
  }
  if (auth.state === 'locked') {
    return <Unlock databasePath={auth.databasePath ?? ''} onUnlocked={reload} />
  }
  return <Shell auth={auth} onChange={reload} />
}
