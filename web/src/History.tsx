import { useCallback, useEffect, useState } from 'react'
import { errorMessage } from './auth.ts'
import { CallList, type Approval } from './Queue.tsx'

export function History({ tick }: { tick: number }) {
  const [items, setItems] = useState<Approval[] | null>(null)
  const [error, setError] = useState('')
  const [confirming, setConfirming] = useState(false)
  const [clearing, setClearing] = useState(false)
  const now = useNow()

  const load = useCallback(async () => {
    const response = await fetch('/api/history')
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setItems((await response.json()) as Approval[])
    setError('')
  }, [])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load()
    }, 0)
    return () => window.clearTimeout(timer)
  }, [load, tick])

  async function clear() {
    setClearing(true)
    setError('')
    const response = await fetch('/api/history', { method: 'DELETE' })
    setClearing(false)
    setConfirming(false)
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    await load()
  }

  if (items == null && !error) {
    return <p>Loading…</p>
  }

  const calls = items ?? []

  return (
    <div className="approvals">
      <div className="queue-head">
        {confirming ? (
          <div className="form-actions">
            <button type="button" className="danger" disabled={clearing} onClick={() => void clear()}>
              Delete these calls
            </button>
            <button type="button" disabled={clearing} onClick={() => setConfirming(false)}>
              Cancel
            </button>
          </div>
        ) : (
          <button type="button" className="danger" disabled={calls.length === 0} onClick={() => setConfirming(true)}>
            Clear all
          </button>
        )}
      </div>
      {error ? <p className="error">{error}</p> : null}
      {calls.length === 0 ? <p>No older tool calls.</p> : null}
      <CallList calls={calls} focusID={null} now={now} timeoutSeconds={0} onDecided={load} />
    </div>
  )
}

function useNow() {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  return now
}
