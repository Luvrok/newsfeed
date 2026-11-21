import { useMemo, useState } from 'react'
import type { SourceStat, SourcesResponse } from '../api'
import { GROUP_ORDER, groupLabel, kindColor, plural } from '../lib'
import type { Tab } from '../types'
import { IconBookmark, IconChevron, IconDot, IconLayers } from './Icons'

interface SidebarProps {
  stats: SourcesResponse | null
  tab: Tab
  onTab: (tab: Tab) => void
  selected: Set<string>
  onToggleSource: (id: string) => void
  onClearSources: () => void
  counts: { all: number; unread: number; saved: number }
  hasMore: boolean
  lastUpdated: Date | null
  onNavigate?: () => void
}

export function Sidebar({
  stats,
  tab,
  onTab,
  selected,
  onToggleSource,
  onClearSources,
  counts,
  hasMore,
  lastUpdated,
  onNavigate,
}: SidebarProps) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

  const groups = useMemo(() => {
    const map = new Map<string, SourceStat[]>()
    for (const source of stats?.sources ?? []) {
      const list = map.get(source.group) ?? []
      list.push(source)
      map.set(source.group, list)
    }
    const keys = [...map.keys()].sort((a, b) => {
      const ia = GROUP_ORDER.indexOf(a)
      const ib = GROUP_ORDER.indexOf(b)
      if (ia !== -1 || ib !== -1) return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib)
      return a.localeCompare(b)
    })
    return keys.map((key) => ({ key, label: groupLabel(key), sources: map.get(key) ?? [] }))
  }, [stats])

  const toggleGroup = (key: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const healthy = stats?.sources.filter((s) => s.status === 'ok').length ?? 0
  const total = stats?.sources.length ?? 0

  return (
    <nav className="sidebar" aria-label="Feed filters">
      <div className="side-section">
        <div className="side-label">Streams</div>
        <button
          className={tab === 'all' ? 'nav-item active' : 'nav-item'}
          onClick={() => {
            onTab('all')
            onNavigate?.()
          }}
        >
          <IconLayers size={16} />
          <span>All stories</span>
          <span className="nav-count">{counts.all}{hasMore ? '+' : ''}</span>
        </button>
        <button
          className={tab === 'unread' ? 'nav-item active' : 'nav-item'}
          onClick={() => {
            onTab('unread')
            onNavigate?.()
          }}
        >
          <IconDot size={16} />
          <span>Unread</span>
          <span className="nav-count">{counts.unread}{hasMore ? '+' : ''}</span>
        </button>
        <button
          className={tab === 'saved' ? 'nav-item active' : 'nav-item'}
          onClick={() => {
            onTab('saved')
            onNavigate?.()
          }}
        >
          <IconBookmark size={16} />
          <span>Saved</span>
          <span className="nav-count">{counts.saved}</span>
        </button>
      </div>

      <div className="side-section">
        <div className="side-label">
          <span>Sources</span>
          {selected.size > 0 ? (
            <button className="side-link" onClick={onClearSources}>
              clear
            </button>
          ) : (
            <span className="side-hint">{total}</span>
          )}
        </div>

        {!stats && <div className="side-skeleton" />}

        {groups.map((group) => {
          const isOpen = !collapsed.has(group.key)
          const activeInGroup = group.sources.filter((s) => selected.has(s.id)).length
          return (
            <div className="source-group" key={group.key}>
              <button className="group-head" onClick={() => toggleGroup(group.key)} aria-expanded={isOpen}>
                <IconChevron size={12} open={isOpen} />
                <span>{group.label}</span>
                <span className="group-count">
                  {activeInGroup > 0 ? `${activeInGroup}/${group.sources.length}` : group.sources.length}
                </span>
              </button>
              {isOpen && (
                <div className="group-body">
                  {group.sources.map((source) => {
                    const active = selected.has(source.id)
                    return (
                      <button
                        key={source.id}
                        className={active ? 'source-row active' : 'source-row'}
                        aria-pressed={active}
                        title={`${source.label} · ${source.status}${source.last_error ? `: ${source.last_error}` : ''}`}
                        onClick={() => {
                          onToggleSource(source.id)
                          onNavigate?.()
                        }}
                      >
                        <span className="kind-dot" style={{ background: kindColor(source.kind) }} />
                        <span className="source-name">{source.label}</span>
                        <span className="source-count">{source.items}</span>
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )
        })}
      </div>

      <div className="side-foot">
        <div className="health-strip" aria-hidden="true">
          {(stats?.sources ?? []).map((source) => (
            <i
              key={source.id}
              className={`tick ${source.status}`}
              title={`${source.label}: ${source.status}`}
            />
          ))}
        </div>
        <div className="foot-line">
          <span className="foot-dot" />
          <span>
            {healthy}/{total} {plural(total, 'source', 'sources')} online
          </span>
        </div>
        <div className="foot-line muted">
          {lastUpdated ? `synced ${lastUpdated.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}` : 'connecting…'}
        </div>
        <div className="foot-legend">
          {['rss', 'telegram', 'hackernews', 'mastodon'].map((kind) => (
            <span key={kind} className="legend-item">
              <i className="kind-dot" style={{ background: kindColor(kind) }} />
              {kind === 'hackernews' ? 'hn' : kind}
            </span>
          ))}
        </div>
      </div>
    </nav>
  )
}
