import type { ReactNode } from 'react'
import { mockPlugin } from './mock.tsx'

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
export type ResourcePlugin = {
  type: string
  label: string
  View: (props: ResourceDetails) => ReactNode
  Form: (props: ResourceValues) => ReactNode
}

export const resourcePlugins: ResourcePlugin[] = [mockPlugin]

export function pluginFor(type: string): ResourcePlugin | undefined {
  return resourcePlugins.find((item) => item.type === type)
}
