import { format } from 'sql-formatter'

export function indentJSON(text: string) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

export function formatSQL(command: string, language: 'mysql' | 'postgresql') {
  const trimmed = command.trim()
  if (trimmed === '') {
    return command
  }
  try {
    return format(trimmed, { language, tabWidth: 2 })
  } catch {
    return command
  }
}
