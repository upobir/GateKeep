import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { errorMessage } from './auth.ts'
import { commandText, resourceLabel, resultText } from './resources/registry.ts'

export type Approval = {
  id: number
  resourceName: string
  resourceType?: string
  resourceConfig?: Record<string, string>
  harnessName: string
  command: string
  reason: string
  status: string
  decision?: string
  decisionReason?: string
  executionStatus?: string
  executionTimeoutSeconds?: number
  result?: string
  createdAt: string
  decidedAt?: string
  finishedAt?: string
}

export function Queue({ focusID, tick }: { focusID: number | null; tick: number }) {
  const [items, setItems] = useState<Approval[] | null>(null)
  const [focused, setFocused] = useState<Approval | null>(null)
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState('')
  const [timeoutSeconds, setTimeoutSeconds] = useState(5 * 60)
  const now = useNow()

  const load = useCallback(async () => {
    try {
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
    } catch {
      setError('Could not load approvals. Check the connection to GateKeep.')
    }
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

  const listed = items ?? []
  const active = listed.filter((call) => call.status === 'pending' || call.status === 'executing' || call.status === 'review')
  const recent = listed.filter((call) => call.status !== 'pending' && call.status !== 'executing' && call.status !== 'review')
  const settled = focused && !listed.some((call) => call.id === focused.id) ? focused : null

  return (
    <div className="approvals">
      {active.length > 0 ? (
        <div className="queue-head">
          <span className="count-pill">{active.length} waiting</span>
        </div>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
      {missing ? <p>That request does not exist.</p> : null}
      {settled ? (
        <ApprovalCard call={settled} current now={now} timeoutSeconds={timeoutSeconds} onDecided={load} />
      ) : null}
      {active.length === 0 ? <p>No pending</p> : null}
      <CallList calls={active} focusID={focusID} now={now} timeoutSeconds={timeoutSeconds} onDecided={load} />
      <div className="recent-rule">Recent</div>
      <CallList calls={recent} focusID={focusID} now={now} timeoutSeconds={timeoutSeconds} onDecided={load} />
    </div>
  )
}

export function CallList({
  calls,
  focusID,
  now,
  timeoutSeconds,
  onDecided,
}: {
  calls: Approval[]
  focusID: number | null
  now: number
  timeoutSeconds: number
  onDecided: () => void | Promise<void>
}) {
  if (calls.length === 0) {
    return null
  }
  return (
    <ul className="approval-list">
      {calls.map((call) => (
        <li key={call.id}>
          <ApprovalCard
            call={call}
            current={call.id === focusID}
            now={now}
            timeoutSeconds={timeoutSeconds}
            onDecided={onDecided}
          />
        </li>
      ))}
    </ul>
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
  onDecided: () => void | Promise<void>
}) {
  const [reason, setReason] = useState('')
  const [pending, setPending] = useState(false)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const waiting = call.status === 'pending'
  const reviewing = call.status === 'review'
  const executing = !reviewing && (call.status === 'executing' || running)
  const open = waiting || executing || reviewing
  const result = successResult(call)
  const notes = callNotes(call)

  async function decide(action: 'approve' | 'results' | 'reject' | 'cancel' | 'send') {
    const startsExecution = action === 'approve' || action === 'results'
    if (startsExecution) {
      setRunning(true)
    } else {
      setPending(true)
    }
    setError('')
    try {
      const response = await fetch(`/api/approvals/${call.id}/${action}`, {
        method: 'POST',
        ...(action === 'reject' || action === 'cancel'
          ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ reason }) }
          : {}),
      })
      if (!response.ok) {
        setError(await errorMessage(response))
        return
      }
      setReason('')
      if (action === 'cancel') setRunning(false)
    } catch {
      setError('Could not reach GateKeep. Check the request status before trying again.')
    } finally {
      if (startsExecution) {
        setRunning(false)
      } else {
        setPending(false)
      }
      try {
        await onDecided()
      } catch {
        setError((current) => current || 'Could not refresh the request status.')
      }
    }
  }

  async function reject(event: FormEvent) {
    event.preventDefault()
    await decide('reject')
  }

  async function cancel(event: FormEvent) {
    event.preventDefault()
    await decide('cancel')
  }

  return (
    <article
      id={`approval-${call.id}`}
      className={['approval-card', open ? 'open' : 'settled', current ? 'current' : ''].filter(Boolean).join(' ')}
    >
      <header className="approval-heading">
        <div>
          <h2>{resourceLabel(call.resourceType, call.resourceName)}</h2>
          <p className="muted">{call.harnessName}</p>
        </div>
        {waiting || executing ? (
          <p className="time-left">{executing ? executionLeft(call, now) : timeLeft(call.createdAt, timeoutSeconds, now)}</p>
        ) : reviewing ? (
          <p className="time-left">Results ready</p>
        ) : (
          <CallBadges call={call} />
        )}
      </header>
      <div className={executing ? 'call-body running' : 'call-body'}>
        <div className="why">
          <span>Why this command was sent</span>
          <strong>{call.reason}</strong>
        </div>
        <pre className="command">{commandText(call.resourceType ?? '', call.command, call.resourceConfig)}</pre>
        {result ? <pre className="result-box">{result}</pre> : null}
        {notes.map((note) => (
          <p className="notice-box" key={note.label}>
            <span>{note.label}</span>
            {note.text}
          </p>
        ))}
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
        <div className="decision-row">
          <button type="button" className="approve" disabled={pending} onClick={() => void decide('approve')}>
            Approve
          </button>
          <button type="button" className="see" disabled={pending} onClick={() => void decide('results')}>
            See results
          </button>
        </div>
      ) : null}
      {reviewing ? (
        <div className="decision-row">
          <button type="button" className="approve" disabled={pending} onClick={() => void decide('send')}>
            Send results
          </button>
        </div>
      ) : null}
      {(waiting && !running) || reviewing ? (
        <form className="reject-row" onSubmit={reject}>
          <label>
            Reject reason
            <textarea
              required
              rows={3}
              placeholder={reviewing ? 'Why this result should not be sent' : 'Why this command should not run'}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
          </label>
          <button type="submit" className="danger" disabled={pending}>
            Reject
          </button>
        </form>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
      {!open ? <DeleteCall id={call.id} onDeleted={onDecided} /> : null}
    </article>
  )
}

function CallBadges({ call }: { call: Approval }) {
  const decision = decisionBadge(call)
  const outcome = outcomeBadge(call)
  const stamp = call.finishedAt || call.decidedAt
  return (
    <div className="call-meta">
      {decision ? <span className={`badge badge-${decision.toLowerCase()}`}>{decision}</span> : null}
      {outcome ? <span className={`badge badge-${outcome.toLowerCase().replace(' ', '-')}`}>{outcome}</span> : null}
      {stamp ? <time className="time-left">{when(stamp)}</time> : null}
    </div>
  )
}

function decisionBadge(call: Approval) {
  if (call.decision === 'approve') {
    return 'Approved'
  }
  if (call.decision === 'expire') {
    return 'Expired'
  }
  if (call.decision === 'reject') {
    return 'Rejected'
  }
  if (call.decision === 'cancel') {
    return 'Cancelled'
  }
  return ''
}

function outcomeBadge(call: Approval) {
  if (call.status === 'cancelled' || call.executionStatus === 'cancelled') {
    return 'Cancelled'
  }
  if (call.executionStatus === 'ok') {
    return 'Success'
  }
  if (call.executionStatus === 'error') {
    return 'Error'
  }
  if (call.executionStatus === 'timeout') {
    return 'Timed Out'
  }
  return ''
}

function successResult(call: Approval) {
  if (call.executionStatus === 'ok' && call.result) {
    return resultText(call.resourceType ?? '', call.result)
  }
  return ''
}

function callNotes(call: Approval) {
  const notes: { label: string; text: string }[] = []
  if (call.decision === 'reject' && call.decisionReason) {
    notes.push({ label: 'Reject reason', text: call.decisionReason })
  }
  if (call.executionStatus === 'cancelled' && call.decisionReason && call.decision !== 'reject') {
    notes.push({ label: 'Cancel reason', text: call.decisionReason })
  }
  if (call.executionStatus === 'error' && call.result) {
    notes.push({ label: 'Error', text: call.result })
  }
  return notes
}

function DeleteCall({ id, onDeleted }: { id: number; onDeleted: () => void | Promise<void> }) {
  const [confirming, setConfirming] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  async function remove() {
    setPending(true)
    setError('')
    try {
      const response = await fetch(`/api/approvals/${id}`, { method: 'DELETE' })
      if (!response.ok) {
        setError(await errorMessage(response))
        return
      }
      await onDeleted()
    } catch {
      setError('Could not delete the call. Check the connection to GateKeep.')
    } finally {
      setPending(false)
    }
  }

  return (
    <div className="delete-call">
      {confirming ? (
        <div className="form-actions">
          <button type="button" className="danger" disabled={pending} onClick={() => void remove()}>
            Delete this call
          </button>
          <button type="button" disabled={pending} onClick={() => setConfirming(false)}>
            Cancel
          </button>
        </div>
      ) : (
        <button type="button" className="quiet" onClick={() => setConfirming(true)}>
          Delete
        </button>
      )}
      {error ? <p className="error">{error}</p> : null}
    </div>
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

function when(value: string) {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return parsed.toLocaleString()
}
