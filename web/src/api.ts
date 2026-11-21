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
