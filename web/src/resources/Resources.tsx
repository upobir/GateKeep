import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage, postJSON } from '../auth.ts'
import { mysqlPlugin } from './mysql.tsx'
import { pluginFor, resourceLabel, resourcePlugins, type ResourcePlugin } from './registry.ts'

// Resource names are used in MCP tool enums and URLs; keep them simple in forms.
const resourceNamePattern = /^[A-Za-z0-9_-]+$/

type Resource = {
  id: number
  name: string
  type: string
  config: Record<string, string>
  hasSecrets: boolean
  secrets?: Record<string, string>
  executionTimeoutSeconds: number | null
  cacheIdleSeconds: number | null
}

type ResourceType = {
  type: string
  available: boolean
  unavailableReason?: string
}

type CacheObject = {
  name: string
  createdAt: string
  updatedAt: string
  expiresAt: string
}

type Screen =
  | { mode: 'list' }
  | { mode: 'view'; resource: Resource }
  | { mode: 'edit'; resource: Resource }
  | { mode: 'create' }

export function Resources() {
  const [resources, setResources] = useState<Resource[]>([])
  const [screen, setScreen] = useState<Screen>({ mode: 'list' })
  const [error, setError] = useState('')

  async function reload() {
    const response = await fetch('/api/resources')
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setResources((await response.json()) as Resource[])
  }

  useEffect(() => {
    reload().catch(() => setError('Could not load resources'))
  }, [])

  const viewed = screen.mode === 'view' || screen.mode === 'edit' ? screen.resource : null
  const viewPlugin = viewed ? pluginFor(viewed.type) : undefined

  return (
    <div className="resources">
      {error ? <p className="error">{error}</p> : null}
      {screen.mode === 'view' && viewed && viewPlugin ? (
        <ResourceView
          resource={viewed}
          plugin={viewPlugin}
          onEdit={() => {
            fetch(`/api/resources/${viewed.id}`)
              .then(async (response) => {
                if (!response.ok) {
                  setError(await errorMessage(response))
                  return
                }
                setScreen({ mode: 'edit', resource: (await response.json()) as Resource })
              })
              .catch(() => setError('Could not load the resource'))
          }}
          onBack={() => setScreen({ mode: 'list' })}
          onDeleted={() => {
            setScreen({ mode: 'list' })
            reload().catch(() => setError('Could not load resources'))
          }}
        />
      ) : null}
      {screen.mode === 'edit' && viewed && viewPlugin ? (
        <ResourceForm
          plugin={viewPlugin}
          resource={viewed}
          onCancel={() => setScreen({ mode: 'view', resource: viewed })}
          onSaved={(saved) => {
            setScreen({ mode: 'view', resource: saved })
            reload().catch(() => setError('Could not load resources'))
          }}
        />
      ) : null}
      {screen.mode === 'create' ? (
        <ResourceForm
          plugin={mysqlPlugin}
          resource={null}
          onCancel={() => setScreen({ mode: 'list' })}
          onSaved={(saved) => {
            setScreen({ mode: 'view', resource: saved })
            reload().catch(() => setError('Could not load resources'))
          }}
        />
      ) : null}
      {screen.mode === 'list' ? (
        <>
          {resources.length === 0 ? <p>No resources yet.</p> : null}
          <div className="resource-list">
            {resources.map((resource) => (
              <button
                key={resource.id}
                type="button"
                className="resource-card"
                onClick={() => setScreen({ mode: 'view', resource })}
              >
                {resourceLabel(resource.type, resource.name)}
              </button>
            ))}
          </div>
          <button type="button" className="primary" onClick={() => setScreen({ mode: 'create' })}>
            Add resource
          </button>
        </>
      ) : null}
    </div>
  )
}

function ResourceView({
  resource,
  plugin,
  onEdit,
  onBack,
  onDeleted,
}: {
  resource: Resource
  plugin: ResourcePlugin
  onEdit: () => void
  onBack: () => void
  onDeleted: () => void
}) {
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [error, setError] = useState('')
  const Details = plugin.View

  async function remove() {
    if (!confirmDelete) {
      setConfirmDelete(true)
      return
    }
    const response = await fetch(`/api/resources/${resource.id}`, { method: 'DELETE' })
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    onDeleted()
  }

  return (
    <div className="resource-view">
      <h2>{resource.name}</h2>
      <dl>
        <div className="detail-field">
          <dt>Type</dt>
          <dd>{plugin.label}</dd>
        </div>
        <Details config={resource.config} hasSecrets={resource.hasSecrets} />
        <div className="detail-field">
          <dt>Execution timeout</dt>
          <dd>{resource.executionTimeoutSeconds != null ? `${resource.executionTimeoutSeconds} seconds` : 'Global timeout'}</dd>
        </div>
        <div className="detail-field">
          <dt>Cache idle</dt>
          <dd>{resource.cacheIdleSeconds != null ? `${resource.cacheIdleSeconds} seconds` : '5 minutes'}</dd>
        </div>
      </dl>
      {error ? <p className="error">{error}</p> : null}
      <div className="form-actions">
        <button type="button" className="primary" onClick={onEdit}>
          Edit
        </button>
        <button type="button" onClick={remove}>
          {confirmDelete ? 'Confirm delete' : 'Delete'}
        </button>
        <button type="button" onClick={onBack}>
          Back
        </button>
      </div>
      <CacheList resourceID={resource.id} />
    </div>
  )
}

function ResourceForm({
  plugin,
  resource,
  onCancel,
  onSaved,
}: {
  plugin: ResourcePlugin
  resource: Resource | null
  onCancel: () => void
  onSaved: (saved: Resource) => void
}) {
  const [selected, setSelected] = useState(plugin)
  const [types, setTypes] = useState<ResourceType[] | null>(null)
  const [name, setName] = useState(resource?.name ?? '')
  const [config, setConfig] = useState<Record<string, string>>(resource?.config ?? {})
  const [secrets, setSecrets] = useState<Record<string, string>>(resource?.secrets ?? {})
  const [timeoutSeconds, setTimeoutSeconds] = useState(
    resource?.executionTimeoutSeconds != null ? String(resource.executionTimeoutSeconds) : '',
  )
  const [cacheIdle, setCacheIdle] = useState(
    resource?.cacheIdleSeconds != null ? String(resource.cacheIdleSeconds) : '',
  )
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [testing, setTesting] = useState(false)
  useEffect(() => {
    let cancelled = false
    fetch('/api/resource-types').then(async (response) => {
      if (!response.ok) {
        throw new Error(await errorMessage(response))
      }
      const values = (await response.json()) as ResourceType[]
      if (!cancelled) {
        setTypes(values)
      }
    }).catch(() => {
      if (!cancelled) {
        setError('Could not check resource availability')
      }
    })
    return () => {
      cancelled = true
    }
  }, [])
  const availability = types?.find((item) => item.type === selected.type)
  const unavailable = !types || !availability?.available
  const Form = selected.Form

  function payload() {
    return {
      id: resource?.id,
      name,
      type: selected.type,
      config,
      secrets,
      executionTimeoutSeconds: timeoutSeconds.trim() === '' ? null : Number(timeoutSeconds),
      cacheIdleSeconds: cacheIdle.trim() === '' ? null : Number(cacheIdle),
    }
  }

  async function testConnection() {
    if (testing) return
    setMessage('')
    setError('')
    if (unavailable) {
      setError(availability?.unavailableReason || 'Resource type is unavailable')
      return
    }
    setTesting(true)
    try {
      const response = await postJSON('/api/resources/test', payload())
      if (!response.ok) {
        setError(await errorMessage(response))
        return
      }
      setMessage('Connection succeeded.')
    } catch {
      setError('Could not test the connection. Please try again.')
    } finally {
      setTesting(false)
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    setError('')
    if (!resource && unavailable) {
      setError(availability?.unavailableReason || 'Resource type is unavailable')
      return
    }
    if (!resourceNamePattern.test(name)) {
      setError('Name may only use letters, digits, underscore, and hyphen.')
      return
    }
    const response = resource
      ? await fetch(`/api/resources/${resource.id}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload()),
        })
      : await postJSON('/api/resources', payload())
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    onSaved((await response.json()) as Resource)
  }

  return (
    <form className="resource-form" onSubmit={save}>
      <label>
        Type
        {resource ? (
          <input value={selected.label} readOnly />
        ) : (
          <select
            value={selected.type}
            onChange={(event) => {
              const next = resourcePlugins.find((item) => item.type === event.target.value)
              if (next) {
                setSelected(next)
                setConfig({})
                setSecrets({})
              }
            }}
          >
            {resourcePlugins.map((item) => {
              const status = types?.find((entry) => entry.type === item.type)
              return (
                <option key={item.type} value={item.type} disabled={!status?.available}>
                  {item.label}{status?.unavailableReason ? ` — ${status.unavailableReason}` : ''}
                </option>
              )
            })}
          </select>
        )}
      </label>
      {availability?.unavailableReason ? <p>{availability.unavailableReason}</p> : null}
      <label>
        Name
        <input
          value={name}
          required
          pattern="[A-Za-z0-9_-]+"
          title="Letters, digits, underscore, and hyphen only."
          onChange={(event) => {
            const next = event.target.value
            if (next === '' || /^[A-Za-z0-9_-]*$/.test(next)) {
              setName(next)
            }
          }}
        />
      </label>
      <Form
        config={config}
        secrets={secrets}
        savedSecrets={resource?.hasSecrets ?? false}
        onConfig={(field, value) => setConfig((current) => ({ ...current, [field]: value }))}
        onSecret={(field, value) => setSecrets((current) => ({ ...current, [field]: value }))}
      />
      <label>
        Execution timeout (seconds)
        <input
          type="number"
          min={1}
          max={86400}
          value={timeoutSeconds}
          placeholder="Global timeout"
          onChange={(event) => setTimeoutSeconds(event.target.value)}
        />
      </label>
      <p>Leave blank to use the global timeout.</p>
      <label>
        Cache idle (seconds)
        <input
          type="number"
          min={1}
          max={86400}
          value={cacheIdle}
          placeholder="300"
          onChange={(event) => setCacheIdle(event.target.value)}
        />
      </label>
      <p>Leave blank for 5 minutes. A cached object is removed after it sits unused this long.</p>
      {error ? <p className="error">{error}</p> : null}
      {message ? <p>{message}</p> : null}
      <div className="form-actions">
        <button type="button" disabled={unavailable || testing} aria-busy={testing} onClick={testConnection}>
          {testing ? <span className="spinner button-spinner" aria-hidden="true" /> : null}
          <span role="status">{testing ? 'Testing connection…' : 'Test connection'}</span>
        </button>
        <button type="submit" disabled={!resource && unavailable}>{resource ? 'Save' : 'Add'}</button>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  )
}

function CacheList({ resourceID }: { resourceID: number }) {
  const [objects, setObjects] = useState<CacheObject[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    let stop = false
    async function load() {
      const response = await fetch(`/api/resources/${resourceID}/cache`)
      if (stop) {
        return
      }
      if (!response.ok) {
        setError(await errorMessage(response))
        return
      }
      const body = (await response.json()) as { objects: CacheObject[] }
      setObjects(body.objects)
      setError('')
    }
    load().catch(() => {
      if (!stop) {
        setError('Could not load the cache')
      }
    })
    const timer = setInterval(() => {
      load().catch(() => {
        if (!stop) {
          setError('Could not load the cache')
        }
      })
    }, 1000)
    return () => {
      stop = true
      clearInterval(timer)
    }
  }, [resourceID])

  async function clear(name: string) {
    setError('')
    const response = await fetch(`/api/resources/${resourceID}/cache/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    })
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setObjects((current) => current.filter((object) => object.name !== name))
  }

  return (
    <section className="cache">
      <h3>Cache</h3>
      {objects.length === 0 ? <p>Nothing is cached.</p> : null}
      {objects.length > 0 ? (
        <table className="cache-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Created</th>
              <th>Updated</th>
              <th>Expires</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {objects.map((object) => (
              <tr key={object.name}>
                <td>{object.name}</td>
                <td>{when(object.createdAt)}</td>
                <td>{when(object.updatedAt)}</td>
                <td>{when(object.expiresAt)}</td>
                <td>
                  <button type="button" onClick={() => clear(object.name).catch(() => setError('Could not clear the cache'))}>
                    Clear
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
    </section>
  )
}

function when(value: string) {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return parsed.toLocaleString()
}
