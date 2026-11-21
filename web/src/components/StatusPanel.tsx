import { useEffect } from 'react'
import type { SourcesResponse } from '../api'
import { formatRelative, intervalLabel, kindLabel, plural } from '../lib'
import { IconClose, IconRefresh, IconSettings } from './Icons'

interface StatusPanelProps {
  open: boolean
  onClose: () => void
  stats: SourcesResponse | null
  now: Date
  refreshingId: string | null
  refreshingAll: boolean
  onRefreshSource: (id: string) => void
  onRefreshAll: () => void
  onManage: () => void
}

const rank: Record<string, number> = { error: 0, stale: 1, never: 2, ok: 3 }

export function StatusPanel({
  open,
  onClose,
  stats,
  now,
  refreshingId,
  refreshingAll,
  onRefreshSource,
  onRefreshAll,
  onManage,
}: StatusPanelProps) {
  useEffect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  const sources = [...(stats?.sources ?? [])].sort((a, b) => {
    const byStatus = (rank[a.status] ?? 9) - (rank[b.status] ?? 9)
    return byStatus !== 0 ? byStatus : a.label.localeCompare(b.label)
  })
  const online = sources.filter((s) => s.status === 'ok').length
  const broken = sources.filter((s) => s.status === 'error').length
  const stale = sources.filter((s) => s.status === 'stale').length

  return (
    <div className="status-overlay" role="dialog" aria-modal="true" aria-label="Source status">
      <div className="status-veil" onClick={onClose} />
      <aside className="status-panel">
        <header className="status-head">
          <div>
            <div className="rail-label">Source status</div>
            <h2>
              {online}
              <span>/{sources.length} online</span>
            </h2>
            <p className="status-summary">
              {broken + stale > 0
                ? `${broken} ${plural(broken, 'failure', 'failures')}, ${stale} stale`
                : 'Every configured source answered on its last poll.'}
            </p>
          </div>
          <button className="icon-btn" onClick={onClose} aria-label="Close source status">
            <IconClose size={18} />
          </button>
        </header>

        <div className="status-list">
          {sources.length === 0 && <div className="status-empty">Waiting for the first poll…</div>}
          {sources.map((source) => (
            <div className={`status-row ${source.status}`} key={source.id}>
              <i className="status-dot" />
              <div className="status-main">
                <div className="status-name">
                  <span>{source.label}</span>
                  <span className="status-kind">{kindLabel(source.kind)}</span>
                </div>
                <div className="status-sub">
                  {source.items} {plural(source.items, 'item', 'items')} · every{' '}
                  {intervalLabel(source.interval_sec)}
                  {source.last_run_at ? (
                    <>
                      {' '}
                      · polled {relative(source.last_run_at, now)} in {source.last_duration_ms} ms
                    </>
                  ) : (
                    ' · waiting for the first poll'
                  )}
                </div>
                {source.last_error && <div className="status-error">{source.last_error}</div>}
              </div>
              <button
                className="status-refresh"
                onClick={() => onRefreshSource(source.id)}
                disabled={refreshingId === source.id}
                aria-label={`Refresh ${source.label}`}
                title={`Refresh ${source.label}`}
              >
                <IconRefresh size={15} className={refreshingId === source.id ? 'spin' : undefined} />
              </button>
            </div>
          ))}
        </div>

        <footer className="status-foot">
          <div className="status-foot-actions">
            <button className="status-refresh-all" onClick={onRefreshAll} disabled={refreshingAll}>
              <IconRefresh size={15} className={refreshingAll ? 'spin' : undefined} />
              {refreshingAll ? 'Polling every source…' : 'Refresh all sources'}
            </button>
            <button
              className="status-manage"
              onClick={() => {
                onClose()
                onManage()
              }}
            >
              <IconSettings size={15} />
              Manage sources
            </button>
          </div>
          <p>
            The scheduler polls in the background, independently of this page. A source that
            fails is retried with backoff; the error above is from its last attempt.
          </p>
        </footer>
      </aside>
    </div>
  )
}

function relative(iso: string, now: Date): string {
  const value = formatRelative(iso, now.getTime())
  return value === 'now' ? 'just now' : `${value} ago`
}
