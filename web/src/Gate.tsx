import { useState, type FormEvent, type ReactNode } from 'react'
import { errorMessage, postJSON } from './auth.ts'

export function Gate({
  title,
  children,
  footer,
}: {
  title: string
  children: ReactNode
  footer?: ReactNode
}) {
  return (
    <div className="gate">
      <section>
        <h1>{title}</h1>
        {children}
      </section>
      {footer ? <div className="gate-footer">{footer}</div> : null}
    </div>
  )
}

export function Setup({ onUnlocked }: { onUnlocked: () => void }) {
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (password !== confirm) {
      setError('passwords do not match')
      return
    }
    setPending(true)
    setError('')
    const response = await postJSON('/api/setup', { password, confirm })
    setPending(false)
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    onUnlocked()
  }

  return (
    <Gate title="Create a password">
      <p>This password encrypts secrets on this computer. It cannot be recovered.</p>
      <form onSubmit={submit}>
        <label>
          Password
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
          Confirm password
          <input
            type="password"
            autoComplete="new-password"
            value={confirm}
            minLength={8}
            required
            onChange={(event) => setConfirm(event.target.value)}
          />
        </label>
        {error ? <p className="error">{error}</p> : <p>At least 8 characters.</p>}
        <button type="submit" disabled={pending}>
          Create password
        </button>
      </form>
    </Gate>
  )
}

export function Unlock({
  databasePath,
  onUnlocked,
}: {
  databasePath: string
  onUnlocked: () => void
}) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const [forgot, setForgot] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setPending(true)
    setError('')
    const response = await postJSON('/api/unlock', { password })
    setPending(false)
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    onUnlocked()
  }

  return (
    <Gate
      title="Unlock GateKeep"
      footer={
        forgot ? (
          <div className="forgot-note">
            <p>There is no password recovery.</p>
            <p>
              {databasePath
                ? 'Quit GateKeep, then delete this database file to reset all saved data:'
                : 'Quit GateKeep, then delete the database file to reset all saved data.'}
            </p>
            {databasePath ? <code>{databasePath}</code> : null}
            <p>Also delete the .db-wal and .db-shm files in the same folder if they are there.</p>
          </div>
        ) : (
          <button type="button" className="text" onClick={() => setForgot(true)}>
            Forgot password?
          </button>
        )
      }
    >
      <form onSubmit={submit}>
        <label>
          Password
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            required
            onChange={(event) => setPassword(event.target.value)}
          />
        </label>
        {error ? <p className="error">{error}</p> : null}
        <button type="submit" disabled={pending}>
          Unlock
        </button>
      </form>
    </Gate>
  )
}
