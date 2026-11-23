import { useEffect } from 'react'
import type { ResourceDetails, ResourceValues } from './registry.ts'
import { indentJSON } from './text.ts'
import { TLSSelect, tlsLabel } from './tls.tsx'

export const mongodbPlugin = {
  type: 'mongodb',
  label: 'MongoDB',
  View({ config, hasSecrets }: ResourceDetails) {
    return (
      <>
        <Field label="Host" value={config.host || '—'} />
        <Field label="Port" value={config.port || '27017'} />
        <Field label="Database" value={config.database || '—'} />
        <Field label="User" value={config.user || '—'} />
        <Field label="Auth database" value={config.authDatabase || 'admin'} />
        <Field label="Password" value={hasSecrets ? '••••••••' : 'Not set'} masked={hasSecrets} />
        <Field label="TLS" value={tlsLabel(config.tls)} />
      </>
    )
  },
  Form({ config, secrets, savedSecrets, onConfig, onSecret }: ResourceValues) {
    useEffect(() => {
      if (!config.port) {
        onConfig('port', '27017')
      }
    }, [config.port, onConfig])
    useEffect(() => {
      if (!config.authDatabase) {
        onConfig('authDatabase', 'admin')
      }
    }, [config.authDatabase, onConfig])
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
            value={config.database ?? ''}
            required
            onChange={(event) => onConfig('database', event.target.value)}
          />
        </label>
        <label>
          User
          <input value={config.user ?? ''} required onChange={(event) => onConfig('user', event.target.value)} />
        </label>
        <label>
          Auth database
          <input
            value={config.authDatabase ?? ''}
            required
            onChange={(event) => onConfig('authDatabase', event.target.value)}
          />
        </label>
        <label>
          Password
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
    return indentJSON(command)
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
