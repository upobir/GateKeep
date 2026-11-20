import { useEffect } from 'react'
import type { ResourceDetails, ResourceValues } from './registry.ts'
import { formatSQL, indentJSON } from './text.ts'

export const mysqlPlugin = {
  type: 'mysql',
  label: 'MySQL',
  View({ config, hasSecrets }: ResourceDetails) {
    return (
      <>
        <Field label="Host" value={config.host || '—'} />
        <Field label="Port" value={config.port || '3306'} />
        <Field label="Database" value={config.database || '—'} />
        <Field label="User" value={config.user || '—'} />
        <Field label="Password" value={hasSecrets ? '••••••••' : 'Not set'} masked={hasSecrets} />
      </>
    )
  },
  Form({ config, secrets, savedSecrets, onConfig, onSecret }: ResourceValues) {
    useEffect(() => {
      if (!config.port) {
        onConfig('port', '3306')
      }
    }, [config.port, onConfig])
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
          Password
          <input
            type="password"
            autoComplete="off"
            value={secrets.password ?? ''}
            placeholder={savedSecrets ? 'Saved. Leave blank to keep it.' : ''}
            onChange={(event) => onSecret('password', event.target.value)}
          />
        </label>
      </>
    )
  },
  commandText(command: string) {
    return formatSQL(command, 'mysql')
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
