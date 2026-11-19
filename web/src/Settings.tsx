import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage, type AuthInfo } from './auth.ts'

export function Settings({ auth, onChange }: { auth: AuthInfo; onChange: () => void }) {
  const [minutes, setMinutes] = useState(String(Math.round(auth.unlockDurationSeconds / 60)))
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  async function save(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    setError('')
    const response = await fetch('/api/settings/unlock-duration', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seconds: Number(minutes) * 60 }),
    })
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setMessage('Saved. Applies the next time you unlock.')
    onChange()
  }

  const until = auth.expiresAt ? new Date(auth.expiresAt).toLocaleTimeString() : ''

  return (
    <div className="settings">
      <form onSubmit={save}>
        {until ? <p>Unlocked until {until}.</p> : null}
        <label>
          Unlock duration (minutes)
          <input
            type="number"
            min={1}
            max={1440}
            required
            value={minutes}
            onChange={(event) => setMinutes(event.target.value)}
          />
        </label>
        {error ? <p className="error">{error}</p> : null}
        {message ? <p>{message}</p> : <p>Applies the next time you unlock.</p>}
        <button type="submit">Save</button>
      </form>
      <ApprovalTimeout />
      <ExecutionTimeout />
    </div>
  )
}

function ApprovalTimeout() {
  const [seconds, setSeconds] = useState('300')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void fetch('/api/settings/approval-timeout')
        .then(async (response) => {
          if (!response.ok) {
            setError(await errorMessage(response))
            return
          }
          const body = (await response.json()) as { seconds: number }
          setSeconds(String(body.seconds))
        })
        .catch(() => setError('Could not load the approval timeout'))
    }, 0)
    return () => window.clearTimeout(timer)
  }, [])

  async function save(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    setError('')
    const response = await fetch('/api/settings/approval-timeout', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seconds: Number(seconds) }),
    })
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setMessage('Saved. Requests already waiting use this timeout too.')
  }

  return (
    <form onSubmit={save}>
      <label>
        Approval timeout (seconds)
        <input
          type="number"
          min={1}
          max={86400}
          required
          value={seconds}
          onChange={(event) => setSeconds(event.target.value)}
        />
      </label>
      {error ? <p className="error">{error}</p> : null}
      {message ? <p>{message}</p> : <p>A request expires if nobody approves it in this time.</p>}
      <button type="submit">Save</button>
    </form>
  )
}

function ExecutionTimeout() {
  const [seconds, setSeconds] = useState('30')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void fetch('/api/settings/execution-timeout')
        .then(async (response) => {
          if (!response.ok) {
            setError(await errorMessage(response))
            return
          }
          const body = (await response.json()) as { seconds: number }
          setSeconds(String(body.seconds))
        })
        .catch(() => setError('Could not load the execution timeout'))
    }, 0)
    return () => window.clearTimeout(timer)
  }, [])

  async function save(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    setError('')
    const response = await fetch('/api/settings/execution-timeout', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seconds: Number(seconds) }),
    })
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setMessage('Saved. The next command uses this limit unless the resource sets its own.')
  }

  return (
    <form onSubmit={save}>
      <label>
        Execution timeout (seconds)
        <input
          type="number"
          min={1}
          max={86400}
          required
          value={seconds}
          onChange={(event) => setSeconds(event.target.value)}
        />
      </label>
      {error ? <p className="error">{error}</p> : null}
      {message ? <p>{message}</p> : <p>A command stops if it runs longer than this. A resource can set its own limit.</p>}
      <button type="submit">Save</button>
    </form>
  )
}
