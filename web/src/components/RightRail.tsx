import type { Bucket, SourcesResponse, Story } from '../api'
import { compactNumber, formatRelative, kindColor, kindLabel } from '../lib'

interface RightRailProps {
  stats: SourcesResponse | null
  latest: Story[]
  now: Date
  onOpen: (story: Story) => void
}

export function Sparkline({ data }: { data: Bucket[] }) {
  const max = Math.max(1, ...data.map((bucket) => bucket.count))
  return (
    <div className="spark" role="img" aria-label="Stories published per hour over the last 24 hours">
      {data.map((bucket) => {
        const height = Math.max(4, Math.round((bucket.count / max) * 100))
        const date = new Date(bucket.t * 1000)
        return (
          <i
            key={bucket.t}
            className={bucket.count === 0 ? 'spark-bar empty' : 'spark-bar'}
            style={{ height: `${height}%` }}
            title={`${bucket.count} ${bucket.count === 1 ? 'story' : 'stories'} · ${date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}`}
          />
        )
      })}
    </div>
  )
}

export function RightRail({ stats, latest, now, onOpen }: RightRailProps) {
  const sources = stats?.sources ?? []
  const online = sources.filter((source) => source.status === 'ok').length
  const failing = sources.filter((source) => source.status === 'error').length
  const avg = sources.length
    ? Math.round(sources.reduce((sum, source) => sum + source.last_duration_ms, 0) / sources.length)
    : 0

  const mix = new Map<string, number>()
  for (const source of sources) {
    mix.set(source.kind, (mix.get(source.kind) ?? 0) + source.items)
  }
  const mixTotal = [...mix.values()].reduce((a, b) => a + b, 0) || 1
  const mixRows = [...mix.entries()].sort((a, b) => b[1] - a[1])

  const total = stats?.items_24h ?? 0

  return (
    <aside className="rail" aria-label="Overview">
      <section className="rail-block">
        <div className="rail-label">
          <span>The wire</span>
          <span className="rail-live">
            <i /> live
          </span>
        </div>
        <div className="latest-list">
          {latest.length === 0 && <div className="rail-empty">Waiting for the first fetch…</div>}
          {latest.map((story) => (
            <button key={story.id} className="latest-item" onClick={() => onOpen(story)}>
              <span className="latest-time">{formatRelative(story.published_at, now.getTime())}</span>
              <span className="latest-main">
                <span className="latest-source">
                  <i className="kind-dot" style={{ background: kindColor(story.kind) }} />
                  {story.source}
                </span>
                <span className="latest-title">{story.title}</span>
              </span>
            </button>
          ))}
        </div>
      </section>

      <section className="rail-block">
        <div className="rail-label">
          <span>Activity</span>
          <span className="rail-muted">24h</span>
        </div>
        <div className="rail-figure">
          {compactNumber(total)} <span>stories in the last day</span>
        </div>
        <Sparkline data={stats?.activity ?? []} />
        <div className="spark-scale">
          <span>24h ago</span>
          <span>now</span>
        </div>
      </section>

      <section className="rail-block">
        <div className="rail-label">
          <span>Mix</span>
          <span className="rail-muted">{sources.length} sources</span>
        </div>
        <div className="mix-bar">
          {mixRows.map(([kind, count]) => (
            <i
              key={kind}
              style={{ width: `${(count / mixTotal) * 100}%`, background: kindColor(kind) }}
              title={`${kindLabel(kind)}: ${count}`}
            />
          ))}
        </div>
        <div className="mix-legend">
          {mixRows.slice(0, 6).map(([kind, count]) => (
            <span key={kind} className="mix-row">
              <i className="kind-dot" style={{ background: kindColor(kind) }} />
              <span className="mix-name">{kindLabel(kind)}</span>
              <span className="mix-count">{count}</span>
            </span>
          ))}
        </div>
      </section>

      <section className="rail-block">
        <div className="rail-label">
          <span>Engine</span>
          <span className="rail-muted">{stats?.version ?? '—'}</span>
        </div>
        <div className="health-row">
          <span>Sources online</span>
          <b className={failing > 0 ? 'health-warn' : 'health-ok'}>
            {online}/{sources.length}
          </b>
        </div>
        <div className="health-row">
          <span>Failing</span>
          <b className={failing > 0 ? 'health-warn' : ''}>{failing}</b>
        </div>
        <div className="health-row">
          <span>Average poll</span>
          <b>{avg} ms</b>
        </div>
        <div className="health-row">
          <span>Items stored</span>
          <b>{compactNumber(stats?.items_total ?? 0)}</b>
        </div>
      </section>

      <div className="rail-foot">
        Public feeds, official APIs and Telegram previews.
        <br />
        No account, no tracking.
      </div>
    </aside>
  )
}
