import { useEffect, useState } from 'react'

export type LiveStatus = {
  server: 'up' | 'reconnecting'
  database: 'up' | 'down' | 'unknown'
}

export function useLiveStatus(onLock: () => void): LiveStatus {
  const [status, setStatus] = useState<LiveStatus>({
    server: 'reconnecting',
    database: 'unknown',
  })

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

    source.addEventListener('status', onStatus)
    source.addEventListener('auth', onAuth)
    source.onerror = () => {
      setStatus((current) => ({ ...current, server: 'reconnecting' }))
    }

    return () => {
      source.removeEventListener('status', onStatus)
      source.removeEventListener('auth', onAuth)
      source.close()
    }
  }, [onLock])

  return status
}
