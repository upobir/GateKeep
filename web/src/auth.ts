export type AuthInfo = {
  state: 'setup' | 'locked' | 'unlocked'
  unlockDurationSeconds: number
  expiresAt?: string
  databasePath?: string
}

export async function getAuth(): Promise<AuthInfo> {
  const response = await fetch('/api/auth')
  if (!response.ok) {
    throw new Error('Could not reach GateKeep')
  }
  return response.json() as Promise<AuthInfo>
}

export async function postJSON(path: string, body: unknown): Promise<Response> {
  return fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export async function errorMessage(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { error?: string }
    if (body.error) {
      return body.error
    }
  } catch {
    // The body was empty or not JSON.
  }
  return 'Request failed'
}
