import { useEffect } from 'react'
import type { ResourceDetails, ResourceValues } from './registry.ts'
import { indentJSON } from './text.ts'
import { TLSSelect, tlsLabel } from './tls.tsx'

function argvLine(command: string) {
  try {
    const args = JSON.parse(command) as unknown
    if (!Array.isArray(args)) {
      return command
    }
    return args
      .map((arg) => {
        if (typeof arg !== 'string') {
          return JSON.stringify(arg)
        }
        if (arg === '' || /\s/.test(arg) || /["\\]/.test(arg)) {
          return JSON.stringify(arg)
        }
        return arg
      })
      .join(' ')
  } catch {
    return command
  }
}

export const redisPlugin = {
  type: 'redis',
  label: 'Redis',
  View({ config, hasSecrets }: ResourceDetails) {
    return (
      <>
        <Field label="Host" value={config.host || '—'} />
        <Field label="Port" value={config.port || '6379'} />
        <Field label="Database" value={config.database ?? '0'} />
        <Field label="User" value={config.user || '—'} />
        <Field label="Password" value={hasSecrets ? '••••••••' : 'Not set'} masked={hasSecrets} />
        <Field label="TLS" value={tlsLabel(config.tls)} />
      </>
    )
  },
  Form({ config, secrets, savedSecrets, onConfig, onSecret }: ResourceValues) {
    useEffect(() => {
      if (!config.port) {
        onConfig('port', '6379')
      }
    }, [config.port, onConfig])
    useEffect(() => {
      if (config.database === undefined || config.database === '') {
        onConfig('database', '0')
      }
    }, [config.database, onConfig])
    return (
      <>
        <label>
          Host
          <input value={config.host ?? ''} required onChange={(event) => onConfig('host', event.target.value)} />
        </label>
        <label>
          Port
          <input
            type="number"
            min={1}
            max={65535}
            value={config.port ?? ''}
            required
            onChange={(event) => onConfig('port', event.target.value)}
          />
        </label>
        <label>
          Database
          <input
            type="number"
            min={0}
            value={config.database ?? ''}
            required
            onChange={(event) => onConfig('database', event.target.value)}
          />
        </label>
        <label>
          User (optional, ACL)
          <input value={config.user ?? ''} onChange={(event) => onConfig('user', event.target.value)} />
        </label>
        <label>
          Password (optional)
          <input
            type="password"
            autoComplete="off"
            value={secrets.password ?? ''}
            placeholder={savedSecrets ? 'Saved. Leave blank to keep it.' : ''}
            onChange={(event) => onSecret('password', event.target.value)}
          />
        </label>
        <TLSSelect value={config.tls} onChange={(value) => onConfig('tls', value)} />
      </>
    )
  },
  commandText(command: string) {
    return argvLine(command)
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
