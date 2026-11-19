import { useEffect, useState } from 'react'

export type LiveStatus = {
  server: 'up' | 'reconnecting'
  database: 'up' | 'down' | 'unknown'
}

export function useLiveStatus(onLock: () => void): { status: LiveStatus; queueTick: number } {
  const [status, setStatus] = useState<LiveStatus>({
    server: 'reconnecting',
    database: 'unknown',
  })
  const [queueTick, setQueueTick] = useState(0)

  useEffect(() => {
    const source = new EventSource('/api/events')

    function onStatus(event: MessageEvent) {
      let database: LiveStatus['database'] = 'down'
      try {
        const body = JSON.parse(event.data) as { database?: string }
        if (body.database === 'up') {
          database = 'up'
        }
      } catch {
        database = 'down'
      }
      setStatus({ server: 'up', database })
    }

    function onAuth(event: MessageEvent) {
      try {
        const body = JSON.parse(event.data) as { state?: string }
        if (body.state === 'locked') {
          onLock()
        }
      } catch {
        onLock()
      }
    }

    function onApprovals() {
      setQueueTick((current) => current + 1)
    }

    source.addEventListener('status', onStatus)
    source.addEventListener('auth', onAuth)
    source.addEventListener('approvals', onApprovals)
    source.onerror = () => {
      setStatus((current) => ({ ...current, server: 'reconnecting' }))
    }

    return () => {
      source.removeEventListener('status', onStatus)
      source.removeEventListener('auth', onAuth)
      source.removeEventListener('approvals', onApprovals)
      source.close()
    }
  }, [onLock])

  return { status, queueTick }
}
