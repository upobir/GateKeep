// TLS modes match tlsMode in the MySQL, Postgres and ClickHouse plugins. A resource saved before this
// option existed has no tls value, which means off.
const modes = [
  { value: 'off', label: 'Off' },
  { value: 'on', label: 'On (verify certificate)' },
  { value: 'skip-verify', label: 'On (skip verification)' },
]

export function tlsLabel(value: string | undefined) {
  return modes.find((mode) => mode.value === (value || 'off'))?.label ?? value ?? 'Off'
}

export function TLSSelect({ value, onChange }: { value: string | undefined; onChange: (value: string) => void }) {
  return (
    <label>
      TLS
      <select value={value || 'off'} onChange={(event) => onChange(event.target.value)}>
        {modes.map((mode) => (
          <option key={mode.value} value={mode.value}>
            {mode.label}
          </option>
        ))}
      </select>
    </label>
  )
}
