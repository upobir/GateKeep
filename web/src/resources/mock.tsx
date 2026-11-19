import type { ResourceDetails, ResourceValues } from './registry.ts'

export const mockPlugin = {
  type: 'mock',
  label: 'Mock',
  View({ config, hasSecrets }: ResourceDetails) {
    return (
      <>
        <Field label="Host" value={config.host || '—'} />
        <Field label="Token" value={hasSecrets ? '••••••••' : 'Not set'} masked={hasSecrets} />
      </>
    )
  },
  Form({ config, secrets, savedSecrets, onConfig, onSecret }: ResourceValues) {
    return (
      <>
        <label>
          Host
          <input
            value={config.host ?? ''}
            required
            onChange={(event) => onConfig('host', event.target.value)}
          />
        </label>
        <label>
          Token
          <input
            autoComplete="off"
            value={secrets.token ?? ''}
            placeholder={savedSecrets ? 'Saved. Leave blank to keep it.' : ''}
            onChange={(event) => onSecret('token', event.target.value)}
          />
        </label>
      </>
    )
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
