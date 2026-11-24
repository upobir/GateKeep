import type { ResourceDetails, ResourceValues } from './registry.ts'
import { indentJSON } from './text.ts'

export const awsCliPlugin = {
  type: 'aws',
  label: 'AWS (CLI)',
  View({ config }: ResourceDetails) {
    return <>
      <div className="detail-field"><dt>Credentials</dt><dd className="secret-mask">••••••••</dd></div>
      <div className="detail-field"><dt>Region</dt><dd>{config.region || 'Set per command'}</dd></div>
    </>
  },
  Form({ config, secrets, savedSecrets, onConfig, onSecret }: ResourceValues) {
    return <>
      {[
        { name: 'accessKeyId', label: 'Access key ID', required: true },
        { name: 'secretAccessKey', label: 'Secret access key', required: true },
        { name: 'sessionToken', label: 'Session token (optional)', required: false },
      ].map(({ name, label, required }) => <label key={name}>
        {label}
        <input type="password" autoComplete="off" value={secrets[name] ?? ''}
          required={required && !savedSecrets}
          onChange={(event) => onSecret(name, event.target.value)} />
      </label>)}
      <label>
        Region (optional)
        <input value={config.region ?? ''} placeholder="us-east-1"
          onChange={(event) => onConfig('region', event.target.value)} />
      </label>
      <p>Requires AWS CLI on GateKeep’s PATH. Local AWS profiles are not used.</p>
    </>
  },
  commandText(command: string) {
    try {
      const args: unknown = JSON.parse(command)
      if (Array.isArray(args) && args.length >= 2 && args.every((arg) => typeof arg === 'string')) {
        const lines: string[][] = [['aws']]
        for (const arg of args) {
          if (arg.startsWith('--')) {
            lines.push([])
          }
          lines[lines.length - 1].push(quoteArgument(arg))
        }
        return lines.map((line, index) => (index === 0 ? '' : '  ') + line.join(' ')).join(' \\\n')
      }
    } catch { /* Show malformed commands verbatim for review. */ }
    return command
  },
  resultText: indentJSON,
}

function quoteArgument(arg: string): string {
  if (/^[A-Za-z0-9_./:=@,-]+$/.test(arg)) {
    return arg
  }
  if (arg.includes("'")) {
    return '"' + arg.replace(/["\\$`]/g, '\\$&') + '"'
  }
  return "'" + arg + "'"
}
