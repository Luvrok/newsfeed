export interface Story {
  id: number
  source_id: string
  source: string
  kind: string
  group: string
  url: string
  title: string
  summary: string
  author: string
  image: string
  content?: string
  content_format?: 'html' | 'text' | ''
  published_at: string
  tags: string[]
}

export interface FeedResponse {
  items: Story[]
  next_cursor: string
  has_more: boolean
  generated_at: string
}

export interface SourceStat {
  id: string
  kind: string
  label: string
  group: string
  site_url?: string
  interval_sec: number
  items: number
  last_run_at?: string
  last_ok_at?: string
  last_error?: string
  last_duration_ms: number
  last_new: number
  runs: number
  errors: number
  status: 'ok' | 'stale' | 'error' | 'never'
}

export interface Bucket {
  t: number
  count: number
}

export interface SourcesResponse {
  items_total: number
  items_24h: number
  last_item_at?: string
  sources: SourceStat[]
  activity: Bucket[]
  fetched_at: string
  version: string
}

export interface FeedParams {
  limit?: number
  cursor?: string
  sources?: string[]
  kinds?: string[]
  q?: string
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
  })
  if (!res.ok) {
    let message = `HTTP ${res.status}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // keep the status message
    }
    const error = new Error(message) as Error & { status?: number }
    error.status = res.status
    throw error
  }
  return (await res.json()) as T
}

export function getFeed(params: FeedParams = {}, signal?: AbortSignal): Promise<FeedResponse> {
  const qs = new URLSearchParams()
  if (params.limit) qs.set('limit', String(params.limit))
  if (params.cursor) qs.set('cursor', params.cursor)
  if (params.sources?.length) qs.set('source', params.sources.join(','))
  if (params.kinds?.length) qs.set('kind', params.kinds.join(','))
  if (params.q) qs.set('q', params.q)
  return request<FeedResponse>(`/api/v1/feed?${qs.toString()}`, { signal })
}

export function getItems(ids: number[], signal?: AbortSignal): Promise<{ items: Story[] }> {
  return request<{ items: Story[] }>(`/api/v1/items?ids=${ids.slice(0, 200).join(',')}`, { signal })
}

export function getStory(id: number, signal?: AbortSignal): Promise<{ item: Story }> {
  return request<{ item: Story }>(`/api/v1/story/${id}`, { signal })
}

export function getSources(signal?: AbortSignal): Promise<SourcesResponse> {
  return request<SourcesResponse>('/api/v1/sources', { signal })
}

export function triggerRefresh(): Promise<{ triggered: number }> {
  return request<{ triggered: number }>('/api/v1/refresh', { method: 'POST' })
}

export function refreshSource(id: string): Promise<{ triggered: number }> {
  return request<{ triggered: number }>(`/api/v1/sources/${encodeURIComponent(id)}/refresh`, {
    method: 'POST',
  })
}

export type FieldType = 'text' | 'number' | 'bool' | 'select' | 'list' | 'secret'

export interface KindField {
  key: string
  label: string
  help?: string
  type: FieldType
  required?: boolean
  default?: unknown
  options?: string[]
}

export interface KindDescriptor {
  kind: string
  title: string
  description: string
  fields: KindField[]
}

export interface ManagedSource {
  id: string
  kind: string
  label: string
  group: string
  site: string
  interval_sec: number
  enabled: boolean
  origin: string
  options: Record<string, unknown>
  status: string
  items: number
  last_run_at?: string
  last_ok_at?: string
  last_error?: string
  last_duration_ms?: number
}

export interface SourcePayload {
  kind?: string
  label?: string
  group?: string
  site?: string
  interval_sec?: number
  enabled?: boolean
  options?: Record<string, unknown>
}

async function requestJSON<T>(url: string, method: string, body: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    let message = `HTTP ${res.status}`
    try {
      const data = (await res.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      // keep status
    }
    throw new Error(message)
  }
  return (await res.json()) as T
}

export function getKinds(): Promise<{ kinds: KindDescriptor[] }> {
  return request<{ kinds: KindDescriptor[] }>('/api/v1/kinds')
}

export function getManagedSources(): Promise<{ sources: ManagedSource[] }> {
  return request<{ sources: ManagedSource[] }>('/api/v1/manage/sources')
}

export function createSource(payload: SourcePayload): Promise<{ source: ManagedSource }> {
  return requestJSON<{ source: ManagedSource }>('/api/v1/sources', 'POST', payload)
}

export function updateSource(id: string, payload: SourcePayload): Promise<{ source: ManagedSource }> {
  return requestJSON<{ source: ManagedSource }>(`/api/v1/sources/${encodeURIComponent(id)}`, 'PATCH', payload)
}

export async function deleteSource(id: string): Promise<void> {
  const res = await fetch(`/api/v1/sources/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    let message = `HTTP ${res.status}`
    try {
      const data = (await res.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      // keep status
    }
    throw new Error(message)
  }
}

export function probeSource(kind: string, options: Record<string, unknown>): Promise<{
  ok: boolean
  count?: number
  titles?: string[]
  error?: string
}> {
  return requestJSON('/api/v1/sources/probe', 'POST', { kind, options })
}

export async function importOPML(file: File): Promise<{
  added: number
  skipped: number
  failed: number
  errors: string[]
}> {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch('/api/v1/import/opml', { method: 'POST', body: form })
  if (!res.ok) {
    let message = `HTTP ${res.status}`
    try {
      const data = (await res.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      // keep status
    }
    throw new Error(message)
  }
  return (await res.json()) as { added: number; skipped: number; failed: number; errors: string[] }
}
