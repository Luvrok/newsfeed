import { useCallback, useEffect, useRef, useState } from 'react'
import {
  createSource,
  deleteSource,
  getKinds,
  getManagedSources,
  importOPML,
  refreshSource,
  updateSource,
  type KindDescriptor,
  type ManagedSource,
  type SourcePayload,
  type SourcesResponse,
} from '../api'
import { useNow } from '../hooks'
import { formatRelative, intervalLabel, kindColor, kindLabel, plural } from '../lib'
import type { Theme } from '../types'
import {
  BrandMark,
  IconArrowLeft,
  IconMoon,
  IconPause,
  IconPencil,
  IconPlay,
  IconPlus,
  IconRefresh,
  IconSun,
  IconTrash,
  IconUpload,
} from './Icons'
import { SourceForm } from './SourceForm'

interface ManagePageProps {
  theme: Theme
  onToggleTheme: () => void
  onBack: () => void
  stats: SourcesResponse | null
  onStatus: () => void
  onChanged: () => void
}

export function ManagePage({ theme, onToggleTheme, onBack, stats, onStatus, onChanged }: ManagePageProps) {
  const now = useNow(30_000)
  const [kinds, setKinds] = useState<KindDescriptor[]>([])
  const [sources, setSources] = useState<ManagedSource[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<ManagedSource | undefined>(undefined)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [importing, setImporting] = useState(false)
  const fileRef = useRef<HTMLInputElement | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await getManagedSources()
      setSources(res.sources)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not read sources')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    getKinds()
      .then((res) => {
        if (!cancelled) setKinds(res.kinds)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Could not read source kinds')
      })
    void load()
    const id = window.setInterval(() => {
      if (!document.hidden) void load()
    }, 20_000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [load])

  useEffect(() => {
    if (!notice) return
    const id = window.setTimeout(() => setNotice(null), 6000)
    return () => window.clearTimeout(id)
  }, [notice])

  const save = async (payload: SourcePayload) => {
    if (editing) {
      await updateSource(editing.id, payload)
    } else {
      await createSource(payload)
    }
    setFormOpen(false)
    setEditing(undefined)
    setNotice(editing ? 'Source updated' : 'Source added')
    await load()
    onChanged()
  }

  const toggle = async (source: ManagedSource) => {
    setBusyId(source.id)
    try {
      await updateSource(source.id, { enabled: !source.enabled })
      await load()
      onChanged()
    } catch (err) {
      setNotice(err instanceof Error ? err.message : 'Could not change the source')
    } finally {
      setBusyId(null)
    }
  }

  const remove = async (source: ManagedSource) => {
    if (!window.confirm(`Remove "${source.label}"? Existing stories stay readable, but polling stops.`)) {
      return
    }
    setBusyId(source.id)
    try {
      await deleteSource(source.id)
      await load()
      onChanged()
      setNotice('Source removed')
    } catch (err) {
      setNotice(err instanceof Error ? err.message : 'Could not remove the source')
    } finally {
      setBusyId(null)
    }
  }

  const refresh = async (source: ManagedSource) => {
    setBusyId(source.id)
    try {
      await refreshSource(source.id)
      await new Promise((resolve) => window.setTimeout(resolve, 1100))
      await load()
      onChanged()
    } catch (err) {
      setNotice(err instanceof Error ? err.message : 'Could not refresh the source')
    } finally {
      setBusyId(null)
    }
  }

  const importFile = async (file: File | undefined) => {
    if (!file) return
    setImporting(true)
    try {
      const result = await importOPML(file)
      setNotice(
        `Imported ${result.added}, skipped ${result.skipped}` +
          (result.failed ? `, failed ${result.failed}` : ''),
      )
      await load()
      onChanged()
    } catch (err) {
      setNotice(err instanceof Error ? err.message : 'Could not import the OPML file')
    } finally {
      setImporting(false)
      if (fileRef.current) fileRef.current.value = ''
    }
  }

  const enabled = sources.filter((source) => source.enabled)
  const paused = sources.filter((source) => !source.enabled)
  const online = stats ? stats.sources.filter((source) => source.status === 'ok').length : 0
  const total = stats?.sources.length ?? 0

  return (
    <>
      <header className="topbar story-topbar">
        <div className="topbar-left">
          <button className="icon-btn" onClick={onBack} aria-label="Back to the feed">
            <IconArrowLeft size={18} />
          </button>
          <button className="brand" onClick={onBack} aria-label="Dispatch, back to the feed">
            <BrandMark size={25} />
            <span className="brand-name">
              Dispatch<span className="brand-dot">.</span>
            </span>
          </button>
          <span className="story-topbar-divider" />
          <button className="story-topbar-back" onClick={onBack}>
            The wire
          </button>
        </div>
        <div className="topbar-right">
          <button
            type="button"
            className={online === total ? 'live-chip' : 'live-chip warn'}
            onClick={onStatus}
            aria-label={`Source status: ${online} of ${total} online`}
            title="Open source status"
          >
            <span className={online === total ? 'live-dot' : 'live-dot warn'} />
            <span className="live-text">{total ? `${online}/${total}` : '—'}</span>
            <span className="live-label">live</span>
          </button>
          <button className="icon-btn" onClick={onToggleTheme} aria-label="Toggle theme">
            {theme === 'dark' ? <IconSun size={17} /> : <IconMoon size={17} />}
          </button>
        </div>
      </header>

      <main className="manage-page">
        <header className="manage-head">
          <div className="kicker">Sources</div>
          <h1>Manage sources</h1>
          <p>
            Add a feed, a Telegram channel or a subreddit. The scheduler starts it immediately;
            no config file and no restart.
          </p>
          <div className="manage-actions">
            <button
              className="primary-btn"
              onClick={() => {
                setEditing(undefined)
                setFormOpen(true)
              }}
            >
              <IconPlus size={15} />
              Add source
            </button>
            <button className="ghost-btn" onClick={() => fileRef.current?.click()} disabled={importing}>
              <IconUpload size={15} />
              {importing ? 'Importing…' : 'Import OPML'}
            </button>
            <input
              ref={fileRef}
              type="file"
              accept=".opml,.xml,text/xml,application/xml"
              hidden
              onChange={(event) => void importFile(event.target.files?.[0])}
            />
            <span className="manage-total">
              {sources.length} {plural(sources.length, 'source', 'sources')} · {enabled.length} active
            </span>
          </div>
        </header>

        {notice && (
          <div className="feed-banner manage-banner">
            <span>{notice}</span>
            <button className="text-btn" onClick={() => setNotice(null)}>
              dismiss
            </button>
          </div>
        )}
        {error && (
          <div className="feed-banner manage-banner">
            <span>{error}</span>
            <button className="text-btn" onClick={() => void load()}>
              retry
            </button>
          </div>
        )}

        {loading && (
          <div className="manage-list">
            {Array.from({ length: 4 }, (_, index) => (
              <div className="manage-row" key={index}>
                <span className="skeleton status-dot" />
                <div>
                  <div className="skeleton" style={{ width: '30%', height: 12 }} />
                  <div className="skeleton" style={{ width: '55%', height: 8, marginTop: 10 }} />
                </div>
              </div>
            ))}
          </div>
        )}

        {!loading && sources.length === 0 && (
          <div className="manage-empty">
            <b>No sources yet</b>
            <p>Add an RSS feed, a Telegram channel or a subreddit, or import a Miniflux OPML file.</p>
          </div>
        )}

        {enabled.length > 0 && (
          <section className="manage-section">
            <h2 className="manage-section-title">Active</h2>
            <div className="manage-list">
              {enabled.map((source) => (
                <SourceRow
                  key={source.id}
                  source={source}
                  now={now}
                  busy={busyId === source.id}
                  onRefresh={() => void refresh(source)}
                  onToggle={() => void toggle(source)}
                  onEdit={() => {
                    setEditing(source)
                    setFormOpen(true)
                  }}
                  onDelete={() => void remove(source)}
                />
              ))}
            </div>
          </section>
        )}

        {paused.length > 0 && (
          <section className="manage-section">
            <h2 className="manage-section-title">Paused</h2>
            <div className="manage-list">
              {paused.map((source) => (
                <SourceRow
                  key={source.id}
                  source={source}
                  now={now}
                  busy={busyId === source.id}
                  onRefresh={() => void refresh(source)}
                  onToggle={() => void toggle(source)}
                  onEdit={() => {
                    setEditing(source)
                    setFormOpen(true)
                  }}
                  onDelete={() => void remove(source)}
                />
              ))}
            </div>
          </section>
        )}
      </main>

      {formOpen && (
        <SourceForm
          key={editing?.id ?? 'new'}
          kinds={kinds}
          initial={editing}
          onSave={save}
          onCancel={() => {
            setFormOpen(false)
            setEditing(undefined)
          }}
        />
      )}
    </>
  )
}

function SourceRow({
  source,
  now,
  busy,
  onRefresh,
  onToggle,
  onEdit,
  onDelete,
}: {
  source: ManagedSource
  now: Date
  busy: boolean
  onRefresh: () => void
  onToggle: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const status = source.enabled ? source.status : 'paused'
  return (
    <div className={`manage-row ${status}`}>
      <i className="status-dot" />
      <div className="manage-main">
        <div className="manage-name">
          <span>{source.label}</span>
          <span className="manage-kind">
            <i className="kind-dot" style={{ background: kindColor(source.kind) }} />
            {kindLabel(source.kind)}
          </span>
          {source.origin !== 'ui' && <span className="manage-origin">{source.origin}</span>}
        </div>
        <div className="manage-meta">
          {source.group || 'No group'} · every {intervalLabel(source.interval_sec)} ·{' '}
          {source.items} {plural(source.items, 'item', 'items')}
          {source.last_run_at ? ` · last poll ${relative(source.last_run_at, now)}` : ''}
        </div>
        {source.last_error && <div className="manage-error">{source.last_error}</div>}
      </div>
      <div className="manage-row-actions">
        <button
          className="manage-action"
          onClick={onRefresh}
          disabled={busy || !source.enabled}
          aria-label={`Refresh ${source.label}`}
          title="Fetch now"
        >
          <IconRefresh size={15} className={busy ? 'spin' : undefined} />
        </button>
        <button
          className="manage-action"
          onClick={onToggle}
          disabled={busy}
          aria-label={source.enabled ? 'Pause source' : 'Resume source'}
          title={source.enabled ? 'Pause' : 'Resume'}
        >
          {source.enabled ? <IconPause size={15} /> : <IconPlay size={15} />}
        </button>
        <button className="manage-action" onClick={onEdit} aria-label={`Edit ${source.label}`} title="Edit">
          <IconPencil size={15} />
        </button>
        <button
          className="manage-action danger"
          onClick={onDelete}
          aria-label={`Delete ${source.label}`}
          title="Delete"
        >
          <IconTrash size={15} />
        </button>
      </div>
    </div>
  )
}

function relative(iso: string, now: Date): string {
  const value = formatRelative(iso, now.getTime())
  return value === 'now' ? 'just now' : `${value} ago`
}
