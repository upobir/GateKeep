import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage, postJSON } from '../auth.ts'
import { pluginFor, resourcePlugins, type ResourcePlugin } from './registry.ts'

type Resource = {
  id: number
  name: string
  type: string
  config: Record<string, string>
  hasSecrets: boolean
  secrets?: Record<string, string>
  executionTimeoutSeconds: number | null
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
          plugin={resourcePlugins[0]}
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
          <ul className="resource-list">
            {resources.map((resource) => (
              <li key={resource.id} className="resource-row">
                <span>{resource.name}</span>
                <span className="status-value">{pluginFor(resource.type)?.label ?? resource.type}</span>
                <button type="button" onClick={() => setScreen({ mode: 'view', resource })}>
                  View
                </button>
              </li>
            ))}
          </ul>
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
  const [name, setName] = useState(resource?.name ?? '')
  const [config, setConfig] = useState<Record<string, string>>(resource?.config ?? {})
  const [secrets, setSecrets] = useState<Record<string, string>>(resource?.secrets ?? {})
  const [timeoutSeconds, setTimeoutSeconds] = useState(
    resource?.executionTimeoutSeconds != null ? String(resource.executionTimeoutSeconds) : '',
  )
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const Form = selected.Form

  function payload() {
    return {
      id: resource?.id,
      name,
      type: selected.type,
      config,
      secrets,
      executionTimeoutSeconds: timeoutSeconds.trim() === '' ? null : Number(timeoutSeconds),
    }
  }

  async function testConnection() {
    setMessage('')
    setError('')
    const response = await postJSON('/api/resources/test', payload())
    if (!response.ok) {
      setError(await errorMessage(response))
      return
    }
    setMessage('Connection succeeded.')
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    setError('')
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
            {resourcePlugins.map((item) => (
              <option key={item.type} value={item.type}>
                {item.label}
              </option>
            ))}
          </select>
        )}
      </label>
      <label>
        Name
        <input value={name} required onChange={(event) => setName(event.target.value)} />
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
      {error ? <p className="error">{error}</p> : null}
      {message ? <p>{message}</p> : null}
      <div className="form-actions">
        <button type="button" onClick={testConnection}>
          Test connection
        </button>
        <button type="submit">{resource ? 'Save' : 'Add'}</button>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  )
}
