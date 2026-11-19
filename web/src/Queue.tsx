import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { errorMessage } from './auth.ts'

type Approval = {
  id: number
  resourceName: string
  harnessName: string
  command: string
  reason: string
  status: string
  decisionReason?: string
  executionStatus?: string
  executionTimeoutSeconds?: number
  result?: string
  createdAt: string
  decidedAt?: string
}

export function Queue({ focusID, tick }: { focusID: number | null; tick: number }) {
  const [items, setItems] = useState<Approval[] | null>(null)
  const [focused, setFocused] = useState<Approval | null>(null)
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState(5 * 60)
  const now = useNow()

  const load = useCallback(async () => {
    const response = await fetch('/api/approvals')
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setItems((await response.json()) as Approval[])
    setError('')
    if (focusID == null) {
      setFocused(null)
      setMissing(false)
      return
    }
    const one = await fetch(`/api/approvals/${focusID}`)
    if (one.status === 404) {
      setFocused(null)
      setMissing(true)
      return
    }
    if (!one.ok) {
      setError(await errorMessage(one))
      return
    }
    setMissing(false)
    setFocused((await one.json()) as Approval)
  }, [focusID])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void load()
      void fetch('/api/settings/approval-timeout')
        .then(async (response) => {
          if (!response.ok) {
            return
          }
          const body = (await response.json()) as { seconds?: number }
          if (body.seconds && body.seconds > 0) {
            setTimeoutSeconds(body.seconds)
          }
        })
        .catch(() => undefined)
    }, 0)
    return () => window.clearTimeout(timer)
  }, [load, tick])

  useEffect(() => {
    if (focusID == null) {
      return
    }
    document.getElementById(`approval-${focusID}`)?.scrollIntoView({ block: 'nearest' })
  }, [focusID, items, focused])

  if (items == null && !error) {
    return <p>Loading…</p>
  }

  const pending = items ?? []
  const settled = focused && focused.status !== 'pending' && focused.status !== 'executing' ? focused : null

  return (
    <div className="approvals">
      {pending.length > 0 ? (
        <div className="queue-head">
          <span className="count-pill">{pending.length} waiting</span>
        </div>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
      {missing ? <p>That request does not exist.</p> : null}
      {settled ? (
        <ApprovalCard call={settled} current now={now} timeoutSeconds={timeoutSeconds} onDecided={load} />
      ) : null}
      {pending.length === 0 && !settled && !missing ? <p>No commands are waiting for approval.</p> : null}
      <ul className="approval-list">
        {pending.map((call) => (
          <li key={call.id}>
            <ApprovalCard
              call={call}
              current={call.id === focusID}
              now={now}
              timeoutSeconds={timeoutSeconds}
              onDecided={load}
            />
          </li>
        ))}
      </ul>
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

function ApprovalCard({
  call,
  current,
  now,
  timeoutSeconds,
  onDecided,
}: {
  call: Approval
  current: boolean
  now: number
  timeoutSeconds: number
  onDecided: () => void
}) {
  const [reason, setReason] = useState('')
  const [pending, setPending] = useState(false)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const waiting = call.status === 'pending'
  const executing = call.status === 'executing' || running

  async function approve() {
    setRunning(true)
    setReason('')
    setError('')
    const response = await fetch(`/api/approvals/${call.id}/approve`, { method: 'POST' })
    setRunning(false)
    if (!response.ok) {
      setError(await errorMessage(response))
      onDecided()
      return
    }
    onDecided()
  }

  async function reject(event: FormEvent) {
    event.preventDefault()
    setPending(true)
    setError('')
    const response = await fetch(`/api/approvals/${call.id}/reject`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reason }),
    })
    setPending(false)
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setReason('')
    onDecided()
  }

  async function cancel(event: FormEvent) {
    event.preventDefault()
    setPending(true)
    setError('')
    const response = await fetch(`/api/approvals/${call.id}/cancel`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reason }),
    })
    setPending(false)
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setReason('')
    setRunning(false)
    onDecided()
  }

  return (
    <article id={`approval-${call.id}`} className={current ? 'approval-card current' : 'approval-card'}>
      <header className="approval-heading">
        <div>
          <h2>{call.resourceName}</h2>
          <p className="muted">{call.harnessName}</p>
        </div>
        <p className="time-left">{executing ? executionLeft(call, now) : waiting ? timeLeft(call.createdAt, timeoutSeconds, now) : statusLine(call)}</p>
      </header>
      <div className={executing ? 'call-body running' : 'call-body'}>
        <div className="why">
          <span>Why this command was sent</span>
          <strong>{call.reason}</strong>
        </div>
        <p className="command">{call.command}</p>
        {executing ? <span className="spinner" role="status" aria-label="Running" /> : null}
      </div>
      {executing ? (
        <form className="reject-row" onSubmit={cancel}>
          <label>
            Cancel reason
            <textarea
              rows={3}
              placeholder="Optional"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
          </label>
          <button type="submit" className="danger" disabled={pending}>
            Cancel
          </button>
        </form>
      ) : null}
      {waiting && !running ? (
        <>
          <div className="decision-row">
            <button type="button" className="approve" disabled={pending} onClick={approve}>
              Approve
            </button>
          </div>
          <form className="reject-row" onSubmit={reject}>
            <label>
              Reject reason
              <textarea
                required
                rows={3}
                placeholder="Why this command should not run"
                value={reason}
                onChange={(event) => setReason(event.target.value)}
              />
            </label>
            <button type="submit" className="danger" disabled={pending}>
              Reject
            </button>
          </form>
        </>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
    </article>
  )
}

function executionLeft(call: Approval, now: number) {
  if (!call.executionTimeoutSeconds || !call.decidedAt) {
    return 'Running'
  }
  return timeLeft(call.decidedAt, call.executionTimeoutSeconds, now)
}

function timeLeft(createdAt: string, timeoutSeconds: number, now: number) {
  const start = new Date(createdAt).getTime()
  if (Number.isNaN(start)) {
    return ''
  }
  const left = Math.ceil((start + timeoutSeconds * 1000 - now) / 1000)
  if (left <= 0) {
    return 'expiring'
  }
  return `${left}s left`
}

function statusLine(call: Approval) {
  const whenDecided = call.decidedAt ? when(call.decidedAt) : ''
  if (call.status === 'rejected') {
    return `Rejected ${whenDecided}. ${call.decisionReason ?? ''}`.trim()
  }
  if (call.status === 'approved' && call.executionStatus === 'ok') {
    return `Approved ${whenDecided}. Result ${call.result ?? ''}`.trim()
  }
  if (call.status === 'cancelled') {
    return `Cancelled ${whenDecided}. ${call.decisionReason ?? ''}`.trim()
  }
  if (call.status === 'approved' && call.executionStatus === 'timeout') {
    return `Approved ${whenDecided}. Execution timed out.`.trim()
  }
  if (call.status === 'approved' && call.executionStatus === 'error') {
    return `Approved ${whenDecided}. ${call.result ?? 'The command failed.'}`.trim()
  }
  if (call.status === 'approved') {
    return `Approved ${whenDecided}. The command has not executed yet.`.trim()
  }
  if (call.status === 'expired') {
    return `Expired ${whenDecided}. ${call.decisionReason ?? ''}`.trim()
  }
  return call.status
}

function when(value: string) {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return parsed.toLocaleString()
}
