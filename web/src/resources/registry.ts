import type { ReactNode } from 'react'
import { clickhousePlugin } from './clickhouse.tsx'
import { httpPlugin } from './http.tsx'
import { mockPlugin } from './mock.tsx'
import { mongodbPlugin } from './mongodb.tsx'
import { mysqlPlugin } from './mysql.tsx'
import { postgresPlugin } from './postgres.tsx'
import { redisPlugin } from './redis.tsx'

export type ResourceValues = {
  config: Record<string, string>
  secrets: Record<string, string>
  savedSecrets: boolean
  onConfig: (name: string, value: string) => void
  onSecret: (name: string, value: string) => void
}

export type ResourceDetails = {
  config: Record<string, string>
  hasSecrets: boolean
}

// A resource type adds its view and form here, and a matching backend plugin.
// commandText and resultText are only for the approval screen.
export type ResourcePlugin = {
  type: string
  label: string
  View: (props: ResourceDetails) => ReactNode
  Form: (props: ResourceValues) => ReactNode
  commandText: (command: string, config: Record<string, string>) => string
  resultText: (result: string) => string
}

export function commandText(type: string, command: string, config: Record<string, string> = {}) {
  return pluginFor(type)?.commandText(command, config) ?? command
}

export function resultText(type: string, result: string) {
  return pluginFor(type)?.resultText(result) ?? result
}

export const resourcePlugins: ResourcePlugin[] = [mysqlPlugin, postgresPlugin, clickhousePlugin, mongodbPlugin, redisPlugin, httpPlugin, mockPlugin]

export function pluginFor(type: string): ResourcePlugin | undefined {
  return resourcePlugins.find((item) => item.type === type)
}

export function resourceLabel(type: string | undefined, name: string) {
  if (!type) {
    return name
  }
  const typeLabel = pluginFor(type)?.label ?? type
  return `${typeLabel} / ${name}`
}
