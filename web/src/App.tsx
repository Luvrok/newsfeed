import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { getItems, triggerRefresh, type Story } from './api'
import { BrandMark, IconClose, IconRefresh } from './components/Icons'
import { Header } from './components/Header'
import { RightRail } from './components/RightRail'
import { Shortcuts } from './components/Shortcuts'
import { Sidebar } from './components/Sidebar'
import { StoryList } from './components/StoryList'
import { useDebounced, useFeed, useLocalStorageSet, useNow, useSources } from './hooks'
import { dedupe, formatClock, plural, sortStories } from './lib'
import type { Tab, Theme } from './types'

function storedTheme(): Theme | null {
  try {
    const value = localStorage.getItem('dispatch.theme')
    return value === 'light' || value === 'dark' ? value : null
  } catch {
    return null
  }
}

function initialTab(): Tab {
  const value = new URLSearchParams(window.location.search).get('tab')
  return value === 'unread' || value === 'saved' ? value : 'all'
}

function initialSearch(): string {
  return new URLSearchParams(window.location.search).get('q') ?? ''
}

function initialSources(): Set<string> {
  const raw = new URLSearchParams(window.location.search).get('source') ?? ''
  return new Set(raw.split(',').map((s) => s.trim()).filter(Boolean))
}

export default function App() {
  const now = useNow(30_000)
  const [theme, setTheme] = useState<Theme>(() => {
    const stored = storedTheme()
    if (stored) return stored
    return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
  })
  const [tab, setTab] = useState<Tab>(initialTab)
  const [search, setSearch] = useState(initialSearch)
  const [selected, setSelected] = useState<Set<string>>(initialSources)
  const [drawer, setDrawer] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [focus, setFocus] = useState(-1)
  const [savedExtra, setSavedExtra] = useState<Story[]>([])

  const searchRef = useRef<HTMLInputElement>(null)
  const refreshingRef = useRef(false)

  const query = useDebounced(search, 280).trim()
  const sourceParam = useMemo(() => [...selected].sort().join(','), [selected])

  const feed = useFeed(query, sourceParam)
  const { data: stats, reload: reloadStats } = useSources()
  const read = useLocalStorageSet('dispatch.read')
  const saved = useLocalStorageSet('dispatch.saved')

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    const meta = document.querySelector('meta[name="theme-color"]')
    if (meta) meta.setAttribute('content', theme === 'dark' ? '#08090a' : '#f5f1e8')
    try {
      localStorage.setItem('dispatch.theme', theme)
    } catch {
      // private mode, ignore
    }
  }, [theme])

  useEffect(() => {
    const params = new URLSearchParams()
    if (tab !== 'all') params.set('tab', tab)
    if (query) params.set('q', query)
    if (selected.size > 0) params.set('source', [...selected].sort().join(','))
    const qs = params.toString()
    history.replaceState(null, '', qs ? `?${qs}` : window.location.pathname)
  }, [tab, query, selected])

  useEffect(() => {
    if (!notice) return
    const id = window.setTimeout(() => setNotice(null), 6000)
    return () => window.clearTimeout(id)
  }, [notice])

  useEffect(() => {
    setFocus(-1)
    window.scrollTo(0, 0)
  }, [query, sourceParam, tab])

  useEffect(() => {
    if (focus < 0) return
    const el = document.querySelector<HTMLElement>(`[data-story-index="${focus}"]`)
    el?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [focus])

  const toggleSource = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const clearSources = useCallback(() => setSelected(new Set()), [])

  const openStory = useCallback(
    (story: Story) => {
      read.add(story.id)
      window.open(story.url, '_blank', 'noopener,noreferrer')
    },
    [read.add],
  )

  const toggleSave = useCallback(
    (story: Story) => {
      saved.toggle(story.id)
    },
    [saved.toggle],
  )

  const refresh = useCallback(async () => {
    if (refreshingRef.current) return
    refreshingRef.current = true
    setRefreshing(true)
    try {
      await triggerRefresh()
      await new Promise((resolve) => window.setTimeout(resolve, 2200))
      feed.reload()
      await reloadStats()
    } catch (err) {
      const status = (err as Error & { status?: number }).status
      setNotice(
        status === 429
          ? 'A refresh was just requested, give the sources a few seconds.'
          : err instanceof Error
            ? err.message
            : 'Could not refresh the sources',
      )
    } finally {
      refreshingRef.current = false
      setRefreshing(false)
    }
  }, [feed.reload, reloadStats])

  useEffect(() => {
    if (tab !== 'saved' || saved.ids.size === 0) {
      setSavedExtra([])
      return
    }
    const controller = new AbortController()
    getItems([...saved.ids], controller.signal)
      .then((res) => setSavedExtra(res.items))
      .catch(() => {
        // the loaded page may still contain some of the bookmarks
      })
    return () => controller.abort()
  }, [tab, saved.ids])

  const visible = useMemo(() => {
    if (tab === 'unread') return feed.items.filter((story) => !read.ids.has(story.id))
    if (tab === 'saved') {
      const needle = query.toLowerCase()
      const pool = dedupe([...feed.items, ...savedExtra]).filter((story) => {
        if (!saved.ids.has(story.id)) return false
        if (selected.size > 0 && !selected.has(story.source_id)) return false
        if (needle && !`${story.title} ${story.summary}`.toLowerCase().includes(needle)) return false
        return true
      })
      return sortStories(pool)
    }
    return feed.items
  }, [feed.items, savedExtra, tab, read.ids, saved.ids, selected, query])

  const counts = useMemo(
    () => ({
      all: feed.items.length,
      unread: feed.items.filter((story) => !read.ids.has(story.id)).length,
      saved: saved.ids.size,
    }),
    [feed.items, read.ids, saved.ids],
  )

  const autoPages = useRef(0)
  useEffect(() => {
    autoPages.current = 0
  }, [query, sourceParam, tab])
  useEffect(() => {
    if (tab !== 'unread') return
    if (feed.loading || feed.loadingMore || !feed.hasMore) return
    if (autoPages.current >= 3) return
    if (counts.unread < 8) {
      autoPages.current += 1
      feed.loadMore()
    }
  }, [tab, feed.loading, feed.loadingMore, feed.hasMore, counts.unread, feed.loadMore])

  const goHome = useCallback(() => {
    setSearch('')
    setSelected(new Set())
    setTab('all')
    setDrawer(false)
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }, [])

  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      const typing =
        !!target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.tagName === 'SELECT' ||
          target.isContentEditable)

      if (typing) {
        if (event.key === 'Escape') target.blur()
        return
      }

      if (event.metaKey || event.ctrlKey) {
        if (event.key.toLowerCase() === 'k') {
          event.preventDefault()
          searchRef.current?.focus()
          searchRef.current?.select()
        }
        if (event.key.toLowerCase() === 'r') {
          event.preventDefault()
          void refresh()
        }
        return
      }
      if (event.altKey) return

      switch (event.key) {
        case 'j':
          event.preventDefault()
          setFocus((current) => (visible.length === 0 ? -1 : Math.min(visible.length - 1, current + 1)))
          break
        case 'k':
          event.preventDefault()
          setFocus((current) => (current <= 0 ? 0 : current - 1))
          break
        case 'Enter':
        case 'o': {
          const story = visible[focus]
          if (story) {
            event.preventDefault()
            openStory(story)
          }
          break
        }
        case 's': {
          const story = visible[focus]
          if (story) {
            event.preventDefault()
            toggleSave(story)
          }
          break
        }
        case '/':
          event.preventDefault()
          searchRef.current?.focus()
          searchRef.current?.select()
          break
        case 'r':
          event.preventDefault()
          void refresh()
          break
        case '?':
          event.preventDefault()
          setShortcuts((value) => !value)
          break
        case 'Escape':
          setShortcuts(false)
          setDrawer(false)
          setFocus(-1)
          break
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [visible, focus, openStory, toggleSave, refresh])

  const dateLine = useMemo(
    () =>
      new Intl.DateTimeFormat(undefined, {
        weekday: 'long',
        day: 'numeric',
        month: 'long',
        year: 'numeric',
      }).format(now),
    [now],
  )

  const heading = query ? 'Search' : tab === 'saved' ? 'Saved' : tab === 'unread' ? 'Unread' : 'Today'
  const showLead = tab === 'all' && !query && selected.size === 0

  return (
    <>
      <Header
        search={search}
        onSearch={setSearch}
        searchRef={searchRef}
        theme={theme}
        onToggleTheme={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
        onRefresh={() => void refresh()}
        refreshing={refreshing}
        sourcesOk={stats ? stats.sources.filter((s) => s.status === 'ok').length : 0}
        sourcesTotal={stats ? stats.sources.length : 0}
        onMenu={() => setDrawer(true)}
        onShortcuts={() => setShortcuts(true)}
        onHome={goHome}
      />

      <div className="app">
        <Sidebar
          stats={stats}
          tab={tab}
          onTab={setTab}
          selected={selected}
          onToggleSource={toggleSource}
          onClearSources={clearSources}
          counts={counts}
          hasMore={feed.hasMore}
          lastUpdated={stats ? new Date(stats.fetched_at) : null}
        />

        <main className="main">
          <div className="feed">
            <header className="feed-head">
              <div className="kicker">
                <span>{dateLine}</span>
                <span className="kicker-dot">·</span>
                <span className="kicker-time">{formatClock(now.toISOString())}</span>
              </div>
              <h1 className="feed-title">{heading}</h1>
              <p className="feed-sub">
                {query ? (
                  <>
                    <b>{visible.length}</b> loaded {plural(visible.length, 'result', 'results')} for
                    “{query}”
                  </>
                ) : tab === 'saved' ? (
                  <>
                    <b>{saved.ids.size}</b> bookmarked {plural(saved.ids.size, 'story', 'stories')} · kept
                    in this browser
                  </>
                ) : tab === 'unread' ? (
                  <>
                    <b>{counts.unread}</b> unread · <b>{feed.items.length}</b> loaded
                  </>
                ) : selected.size > 0 ? (
                  <>
                    Filtered to <b>{selected.size}</b> {plural(selected.size, 'source', 'sources')} ·{' '}
                    <b>{feed.items.length}</b> loaded
                  </>
                ) : stats ? (
                  <>
                    <b>{stats.items_total.toLocaleString()}</b> stories from{' '}
                    <b>{stats.sources.length}</b> sources, newest first
                  </>
                ) : (
                  'Connecting to the wire…'
                )}
              </p>
            </header>

            <div className="tabs" role="tablist" aria-label="Streams">
              {(
                [
                  ['all', 'All', counts.all],
                  ['unread', 'Unread', counts.unread],
                  ['saved', 'Saved', counts.saved],
                ] as Array<[Tab, string, number]>
              ).map(([key, label, count]) => (
                <button
                  key={key}
                  role="tab"
                  aria-selected={tab === key}
                  className={tab === key ? 'tab active' : 'tab'}
                  onClick={() => setTab(key)}
                >
                  {label}
                  <span className="tab-count">
                    {key === 'saved' ? count : `${count}${feed.hasMore ? '+' : ''}`}
                  </span>
                </button>
              ))}
              <span className="tabs-spacer" />
              {stats && (
                <span className="tabs-meta">
                  {stats.items_24h.toLocaleString()} in the last 24h
                </span>
              )}
            </div>

            {notice && (
              <div className="feed-banner">
                <span>{notice}</span>
                <button className="icon-btn" onClick={() => setNotice(null)} aria-label="Dismiss">
                  <IconClose size={14} />
                </button>
              </div>
            )}

            {feed.pending.length > 0 && (
              <button className="new-pill" onClick={feed.mergePending}>
                <IconRefresh size={13} />
                {feed.pending.length} new {plural(feed.pending.length, 'story', 'story')}
              </button>
            )}

            <StoryList
              items={visible}
              now={now}
              read={read}
              saved={saved}
              onOpen={openStory}
              onToggleSave={toggleSave}
              focus={focus}
              showLead={showLead}
              loading={feed.loading}
              loadingMore={feed.loadingMore}
              hasMore={tab === 'saved' ? false : feed.hasMore}
              error={feed.error}
              tab={tab}
              onLoadMore={feed.loadMore}
              onRetry={feed.reload}
              onReset={goHome}
            />

            <footer className="page-foot">
              <span>Dispatch</span>
              <span className="page-foot-rule" />
              <span>{stats?.version ? `engine ${stats.version}` : 'engine —'}</span>
              <span className="page-foot-gap" />
              <span>Read state and bookmarks stay in this browser.</span>
            </footer>
          </div>
        </main>

        <RightRail stats={stats} latest={feed.items.slice(0, 6)} now={now} onOpen={openStory} />
      </div>

      {drawer && (
        <div className="drawer" role="dialog" aria-modal="true" aria-label="Sources">
          <div className="drawer-veil" onClick={() => setDrawer(false)} />
          <div className="drawer-panel">
            <div className="drawer-head">
              <span className="brand">
                <BrandMark size={21} />
                <span className="brand-name">
                  Dispatch<span className="brand-dot">.</span>
                </span>
              </span>
              <button className="icon-btn" onClick={() => setDrawer(false)} aria-label="Close sources">
                <IconClose size={18} />
              </button>
            </div>
            <Sidebar
              stats={stats}
              tab={tab}
              onTab={setTab}
              selected={selected}
              onToggleSource={toggleSource}
              onClearSources={clearSources}
              counts={counts}
              hasMore={feed.hasMore}
              lastUpdated={stats ? new Date(stats.fetched_at) : null}
              onNavigate={() => setDrawer(false)}
            />
          </div>
        </div>
      )}

      <Shortcuts open={shortcuts} onClose={() => setShortcuts(false)} />
    </>
  )
}
