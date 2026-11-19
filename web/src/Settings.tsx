import { useState, type FormEvent } from 'react'
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
    <form className="settings" onSubmit={save}>
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
  )
}
