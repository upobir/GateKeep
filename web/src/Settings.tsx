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
      <RecentWindow />
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

function RecentWindow() {
  const [count, setCount] = useState('1')
  const [unit, setUnit] = useState('hours')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void fetch('/api/settings/recent-window')
        .then(async (response) => {
          if (!response.ok) {
            setError(await errorMessage(response))
            return
          }
          const body = (await response.json()) as { count: number; unit: string }
          setCount(String(body.count))
          setUnit(body.unit)
        })
        .catch(() => setError('Could not load the recent window'))
    }, 0)
    return () => window.clearTimeout(timer)
  }, [])

  async function save(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    setError('')
    const response = await fetch('/api/settings/recent-window', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ count: Number(count), unit }),
    })
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setMessage('Saved. The Approval Queue and History use this window now.')
  }

  return (
    <form onSubmit={save}>
      <fieldset className="recent-window">
        <legend>Recent tool calls</legend>
        <div className="recent-fields">
          <label>
            Last
            <input
              type="number"
              min={1}
              max={1000000}
              required
              value={count}
              onChange={(event) => setCount(event.target.value)}
            />
          </label>
          <label>
            Unit
            <select value={unit} onChange={(event) => setUnit(event.target.value)}>
              <option value="seconds">seconds</option>
              <option value="minutes">minutes</option>
              <option value="hours">hours</option>
              <option value="days">days</option>
              <option value="calls">tool calls</option>
            </select>
          </label>
        </div>
      </fieldset>
      {error ? <p className="error">{error}</p> : null}
      {message ? <p>{message}</p> : <p>{recentHelp(unit)}</p>}
      <button type="submit">Save</button>
    </form>
  )
}

function recentHelp(unit: string) {
  if (unit === 'calls') {
    return 'That many of the latest finished calls stay on the Approval Queue. Older calls move to History.'
  }
  const boundary = unit === 'days' ? 'midnight' : unit === 'hours' ? 'hour' : unit === 'minutes' ? 'minute' : 'second'
  return `The window starts at the beginning of the current ${boundary} and includes the previous ones. Older calls move to History.`
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
