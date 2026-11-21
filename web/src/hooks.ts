import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { getFeed, getSources, type SourcesResponse, type Story } from './api'
import { dedupe, sortStories } from './lib'

export function useNow(interval = 30_000): Date {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), interval)
    return () => window.clearInterval(id)
  }, [interval])
  return now
}

export function useDebounced<T>(value: T, delay = 300): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setDebounced(value), delay)
    return () => window.clearTimeout(id)
  }, [value, delay])
  return debounced
}

function readSet(key: string): Set<number> {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return new Set()
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return new Set()
    return new Set(parsed.filter((v): v is number => typeof v === 'number'))
  } catch {
    return new Set()
  }
}

export interface IdSet {
  ids: Set<number>
  has: (id: number) => boolean
  add: (id: number) => void
  remove: (id: number) => void
  toggle: (id: number) => void
  clear: () => void
  size: number
}

export function useLocalStorageSet(key: string): IdSet {
  const [ids, setIds] = useState<Set<number>>(() => readSet(key))
  const first = useRef(true)

  useEffect(() => {
    if (first.current) {
      first.current = false
      return
    }
    try {
      localStorage.setItem(key, JSON.stringify([...ids]))
    } catch {
      // storage may be full or disabled; the UI still works for this session
    }
  }, [key, ids])

  const has = useCallback((id: number) => ids.has(id), [ids])
  const add = useCallback((id: number) => {
    setIds((prev) => {
      if (prev.has(id)) return prev
      const next = new Set(prev)
      next.add(id)
      return next
    })
  }, [])
  const remove = useCallback((id: number) => {
    setIds((prev) => {
      if (!prev.has(id)) return prev
      const next = new Set(prev)
      next.delete(id)
      return next
    })
  }, [])
  const toggle = useCallback((id: number) => {
    setIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])
  const clear = useCallback(() => setIds(new Set()), [])

  return { ids, has, add, remove, toggle, clear, size: ids.size }
}

export interface SourcesState {
  data: SourcesResponse | null
  error: string | null
  reload: () => Promise<SourcesResponse | null>
}

export function useSources(pollMs = 30_000): SourcesState {
  const [data, setData] = useState<SourcesResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const res = await getSources()
      setData(res)
      setError(null)
      return res
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not read source state')
      return null
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    const tick = async () => {
      try {
        const res = await getSources()
        if (cancelled) return
        setData(res)
        setError(null)
      } catch (err) {
        if (cancelled) return
        setError(err instanceof Error ? err.message : 'Could not read source state')
      }
    }
    void tick()
    const id = window.setInterval(tick, pollMs)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [pollMs])

  return { data, error, reload }
}

export interface FeedState {
  items: Story[]
  pending: Story[]
  loading: boolean
  loadingMore: boolean
  hasMore: boolean
  error: string | null
  loadMore: () => void
  reload: () => void
  mergePending: () => void
}

export function useFeed(query: string, sourceParam: string): FeedState {
  const sources = useMemo(() => (sourceParam ? sourceParam.split(',') : []), [sourceParam])
  const [items, setItems] = useState<Story[]>([])
  const [pending, setPending] = useState<Story[]>([])
  const [cursor, setCursor] = useState('')
  const [hasMore, setHasMore] = useState(true)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const generation = useRef(0)
  const loadingMoreRef = useRef(false)
  const seenRef = useRef<Set<number>>(new Set())

  useEffect(() => {
    const controller = new AbortController()
    generation.current += 1
    const gen = generation.current
    loadingMoreRef.current = false
    seenRef.current = new Set()
    setLoading(true)
    setLoadingMore(false)
    setError(null)
    setItems([])
    setPending([])
    setCursor('')
    setHasMore(true)

    getFeed({ limit: 40, q: query, sources }, controller.signal)
      .then((res) => {
        if (gen !== generation.current) return
        res.items.forEach((item) => seenRef.current.add(item.id))
        setItems(res.items)
        setCursor(res.next_cursor)
        setHasMore(res.has_more)
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted || gen !== generation.current) return
        setError(err instanceof Error ? err.message : 'Could not reach the service')
      })
      .finally(() => {
        if (gen === generation.current) setLoading(false)
      })

    return () => controller.abort()
  }, [query, sources, reloadKey])

  const loadMore = useCallback(() => {
    if (loadingMoreRef.current || !hasMore || !cursor || loading) return
    const gen = generation.current
    loadingMoreRef.current = true
    setLoadingMore(true)
    getFeed({ limit: 40, cursor, q: query, sources })
      .then((res) => {
        if (gen !== generation.current) return
        res.items.forEach((item) => seenRef.current.add(item.id))
        setItems((prev) => dedupe([...prev, ...res.items]))
        setCursor(res.next_cursor)
        setHasMore(res.has_more)
      })
      .catch((err: unknown) => {
        if (gen !== generation.current) return
        setError(err instanceof Error ? err.message : 'Could not load more stories')
      })
      .finally(() => {
        if (gen !== generation.current) return
        loadingMoreRef.current = false
        setLoadingMore(false)
      })
  }, [cursor, hasMore, loading, query, sources])

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  const mergePending = useCallback(() => {
    if (pending.length === 0) return
    setItems((prev) => sortStories(dedupe([...pending, ...prev])))
    setPending([])
  }, [pending])

  useEffect(() => {
    let cancelled = false
    const tick = async () => {
      if (document.hidden) return
      try {
        const res = await getFeed({ limit: 20, q: query, sources })
        if (cancelled) return
        const fresh = res.items.filter((item) => !seenRef.current.has(item.id))
        if (fresh.length === 0) return
        fresh.forEach((item) => seenRef.current.add(item.id))
        setPending((prev) => dedupe([...fresh, ...prev]))
      } catch {
        // polling is best effort; the visible error comes from explicit loads
      }
    }
    const id = window.setInterval(tick, 60_000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [query, sources])

  return { items, pending, loading, loadingMore, hasMore, error, loadMore, reload, mergePending }
}
