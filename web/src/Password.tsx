import { useState, type FormEvent } from 'react'
import { errorMessage, postJSON } from './auth.ts'

export function Password() {
  const [current, setCurrent] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [pending, setPending] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    if (password !== confirm) {
      setError('passwords do not match')
      return
    }
    setPending(true)
    setError('')
    const response = await postJSON('/api/password', { current, password, confirm })
    setPending(false)
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setCurrent('')
    setPassword('')
    setConfirm('')
    setMessage('Password updated.')
  }

  return (
    <form className="settings" onSubmit={submit}>
      <p>This re-encrypts saved secrets.</p>
      <label>
        Current password
        <input
          type="password"
          autoComplete="current-password"
          value={current}
          required
          onChange={(event) => setCurrent(event.target.value)}
        />
      </label>
      <label>
        New password
        <input
          type="password"
          autoComplete="new-password"
          value={password}
          minLength={8}
          required
          onChange={(event) => setPassword(event.target.value)}
        />
      </label>
      <label>
        Confirm new password
        <input
          type="password"
          autoComplete="new-password"
          value={confirm}
          minLength={8}
          required
          onChange={(event) => setConfirm(event.target.value)}
        />
      </label>
      {error ? <p className="error">{error}</p> : null}
      {message ? <p>{message}</p> : <p>At least 8 characters.</p>}
      <button type="submit" disabled={pending}>
        Update password
      </button>
    </form>
  )
}
