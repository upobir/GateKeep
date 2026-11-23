import { useEffect } from 'react'
import type { ResourceDetails, ResourceValues } from './registry.ts'
import { indentJSON } from './text.ts'
import { TLSSelect, tlsLabel } from './tls.tsx'

// Auth values match readTarget in internal/plugin/httpapi.
const authModes = [
  { value: 'none', label: 'None' },
  { value: 'api-key', label: 'API key' },
  { value: 'bearer', label: 'Bearer token' },
  { value: 'basic', label: 'Basic auth' },
]

function authLabel(value: string | undefined) {
  return authModes.find((mode) => mode.value === (value || 'none'))?.label ?? value ?? 'None'
}

type Request = {
  method?: unknown
  path?: unknown
  params?: unknown
  headers?: unknown
  body?: unknown
}

function stringMap(value: unknown): [string, string][] {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return []
  }
  return Object.entries(value as Record<string, unknown>).map(([key, item]) => [
    key,
    typeof item === 'string' ? item : JSON.stringify(item),
  ])
}

// requestText shows the command as an HTTP message with params and form
// fields one per line, decoded. The resource's credentials are never shown.
function requestText(command: string, config: Record<string, string>) {
  let call: Request
  try {
    call = JSON.parse(command) as Request
  } catch {
    return command
  }
  if (!call || typeof call !== 'object' || Array.isArray(call)) {
    return command
  }
  const sections: string[] = []

  const top = [`${String(call.method ?? '').toUpperCase()} ${String(call.path ?? '')}`]
  for (const [key, value] of stringMap(call.params)) {
    top.push(`${key}=${value}`)
  }
  sections.push(top.join('\n'))

  const headers = stringMap(call.headers)
  const headerLines = [`Host: ${config.host || '(resource host)'}`]
  const authHeader = config.auth === 'api-key' ? config.apiKeyHeader : config.auth === 'bearer' || config.auth === 'basic' ? 'Authorization' : ''
  if (authHeader) {
    headerLines.push(`${authHeader}: <resource auth>`)
  }
  const hasBody = call.body !== undefined && call.body !== null
  const contentType = headers.find(([key]) => key.toLowerCase() === 'content-type')?.[1]
  for (const [key, value] of headers) {
    headerLines.push(`${key}: ${value}`)
  }
  if (hasBody && !contentType) {
    headerLines.push('Content-Type: text/plain')
  }
  sections.push(headerLines.join('\n'))

  if (hasBody) {
    sections.push(bodyText(call.body, contentType ?? ''))
  }
  return sections.join('\n\n')
}

function bodyText(body: unknown, contentType: string) {
  const mediaType = contentType.split(';')[0].trim().toLowerCase()
  if (mediaType === 'application/x-www-form-urlencoded') {
    return stringMap(body)
      .map(([key, value]) => `${key}=${value}`)
      .join('\n')
  }
  if (typeof body === 'string') {
    return body
  }
  return JSON.stringify(body, null, 2)
}

export const httpPlugin = {
  type: 'http',
  label: 'HTTP',
  View({ config, hasSecrets }: ResourceDetails) {
    const secret = hasSecrets ? '••••••••' : 'Not set'
    return (
      <>
        <Field label="Host" value={config.host || '—'} />
        <Field label="TLS" value={tlsLabel(config.tls)} />
        <Field label="Authentication" value={authLabel(config.auth)} />
        {config.auth === 'api-key' ? (
          <>
            <Field label="API key header" value={config.apiKeyHeader || '—'} />
            <Field label="API key" value={secret} masked={hasSecrets} />
          </>
        ) : null}
        {config.auth === 'bearer' ? <Field label="Token" value={secret} masked={hasSecrets} /> : null}
        {config.auth === 'basic' ? (
          <>
            <Field label="Username" value={config.username || '—'} />
            <Field label="Password" value={secret} masked={hasSecrets} />
          </>
        ) : null}
      </>
    )
  },
  Form({ config, secrets, savedSecrets, onConfig, onSecret }: ResourceValues) {
    useEffect(() => {
      if (!config.tls) {
        onConfig('tls', 'on')
      }
    }, [config.tls, onConfig])
    useEffect(() => {
      if (!config.auth) {
        onConfig('auth', 'none')
      }
    }, [config.auth, onConfig])
    const saved = savedSecrets ? 'Saved. Leave blank to keep it.' : ''
    return (
      <>
        <label>
          Host
          <input
            value={config.host ?? ''}
            required
            placeholder="api.example.com or host:port"
            onChange={(event) => onConfig('host', event.target.value)}
          />
        </label>
        <TLSSelect value={config.tls} onChange={(value) => onConfig('tls', value)} />
        <label>
          Authentication
          <select value={config.auth || 'none'} onChange={(event) => onConfig('auth', event.target.value)}>
            {authModes.map((mode) => (
              <option key={mode.value} value={mode.value}>
                {mode.label}
              </option>
            ))}
          </select>
        </label>
        {config.auth === 'api-key' ? (
          <>
            <label>
              API key header
              <input
                value={config.apiKeyHeader ?? ''}
                required
                placeholder="X-API-Key"
                onChange={(event) => onConfig('apiKeyHeader', event.target.value)}
              />
            </label>
            <label>
              API key
              <input
                type="password"
                autoComplete="off"
                value={secrets.apiKey ?? ''}
                placeholder={saved}
                onChange={(event) => onSecret('apiKey', event.target.value)}
              />
            </label>
          </>
        ) : null}
        {config.auth === 'bearer' ? (
          <label>
            Token
            <input
              type="password"
              autoComplete="off"
              value={secrets.token ?? ''}
              placeholder={saved}
              onChange={(event) => onSecret('token', event.target.value)}
            />
          </label>
        ) : null}
        {config.auth === 'basic' ? (
          <>
            <label>
              Username
              <input
                value={config.username ?? ''}
                required
                onChange={(event) => onConfig('username', event.target.value)}
              />
            </label>
            <label>
              Password
              <input
                type="password"
                autoComplete="off"
                value={secrets.password ?? ''}
                placeholder={saved}
                onChange={(event) => onSecret('password', event.target.value)}
              />
            </label>
          </>
        ) : null}
      </>
    )
  },
  commandText(command: string, config: Record<string, string>) {
    return requestText(command, config)
  },
  resultText(result: string) {
    return indentJSON(result)
  },
}

function Field({ label, value, masked }: { label: string; value: string; masked?: boolean }) {
  return (
    <div className="detail-field">
      <dt>{label}</dt>
      <dd className={masked ? 'secret-mask' : undefined}>{value}</dd>
    </div>
  )
}
