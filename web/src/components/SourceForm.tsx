import { useEffect, useMemo, useState } from 'react'
import {
  probeSource,
  type KindDescriptor,
  type KindField,
  type ManagedSource,
  type SourcePayload,
} from '../api'
import { IconClose } from './Icons'

interface SourceFormProps {
  kinds: KindDescriptor[]
  initial?: ManagedSource
  onSave: (payload: SourcePayload) => Promise<void>
  onCancel: () => void
}

const INTERVALS = [120, 300, 600, 900, 1800, 3600, 21600, 86400]

function defaultsFor(kind: string, kinds: KindDescriptor[]): Record<string, unknown> {
  const desc = kinds.find((item) => item.kind === kind)
  const out: Record<string, unknown> = {}
  for (const field of desc?.fields ?? []) {
    if (field.default !== undefined && field.default !== null) {
      out[field.key] = field.default
    } else if (field.type === 'bool') {
      out[field.key] = false
    }
  }
  return out
}

function fieldText(_field: KindField, value: unknown): string {
  if (Array.isArray(value)) return value.join(', ')
  if (value === undefined || value === null) return ''
  return String(value)
}

function cleanOptions(kind: string, kinds: KindDescriptor[], options: Record<string, unknown>) {
  const desc = kinds.find((item) => item.kind === kind)
  const out: Record<string, unknown> = {}
  for (const field of desc?.fields ?? []) {
    const raw = options[field.key]
    if (field.type === 'list') {
      const values = Array.isArray(raw)
        ? raw.map(String)
        : String(raw ?? '')
            .split(',')
            .map((value) => value.trim())
      const filtered = values.map((value) => value.trim()).filter(Boolean)
      if (filtered.length > 0) out[field.key] = filtered
      continue
    }
    if (field.type === 'number') {
      if (raw === '' || raw === undefined || raw === null) continue
      const number = Number(raw)
      if (Number.isFinite(number)) out[field.key] = number
      continue
    }
    if (field.type === 'bool') {
      out[field.key] = Boolean(raw)
      continue
    }
    if (raw !== undefined && raw !== null && String(raw) !== '') out[field.key] = raw
  }
  return out
}

export function SourceForm({ kinds, initial, onSave, onCancel }: SourceFormProps) {
  const [kind, setKind] = useState(initial?.kind ?? kinds[0]?.kind ?? 'rss')
  const [label, setLabel] = useState(initial?.label ?? '')
  const [group, setGroup] = useState(initial?.group ?? 'Imported')
  const [site, setSite] = useState(initial?.site ?? '')
  const [intervalSec, setIntervalSec] = useState(initial?.interval_sec ?? 600)
  const [enabled, setEnabled] = useState(initial?.enabled ?? true)
  const [options, setOptions] = useState<Record<string, unknown>>(
    () => initial?.options ?? defaultsFor(initial?.kind ?? kinds[0]?.kind ?? 'rss', kinds),
  )
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [probe, setProbe] = useState<{
    ok: boolean
    count?: number
    titles?: string[]
    error?: string
  } | null>(null)

  const descriptor = useMemo(() => kinds.find((item) => item.kind === kind), [kinds, kind])
  const intervalChoices = useMemo(() => {
    const values = [...INTERVALS]
    if (!values.includes(intervalSec)) values.push(intervalSec)
    return values.sort((a, b) => a - b)
  }, [intervalSec])

  useEffect(() => {
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onCancel()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      document.body.style.overflow = previous
      window.removeEventListener('keydown', onKey)
    }
  }, [onCancel])

  const changeKind = (next: string) => {
    setKind(next)
    setOptions(defaultsFor(next, kinds))
    setProbe(null)
  }

  const setOption = (key: string, value: unknown) => {
    setOptions((prev) => ({ ...prev, [key]: value }))
  }

  const test = async () => {
    setTesting(true)
    setError(null)
    setProbe(null)
    try {
      const result = await probeSource(kind, cleanOptions(kind, kinds, options))
      setProbe(result)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Probe failed')
    } finally {
      setTesting(false)
    }
  }

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setError(null)
    try {
      await onSave({
        kind,
        label: label.trim() || descriptor?.title || kind,
        group: group.trim() || 'Imported',
        site: site.trim(),
        interval_sec: intervalSec,
        enabled,
        options: cleanOptions(kind, kinds, options),
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save the source')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="manage-overlay" onClick={onCancel}>
      <form className="source-form" onClick={(event) => event.stopPropagation()} onSubmit={submit}>
        <header className="source-form-head">
          <div>
            <div className="rail-label">{initial ? 'Edit source' : 'New source'}</div>
            <h2>{descriptor?.title ?? 'Source'}</h2>
          </div>
          <button type="button" className="icon-btn" onClick={onCancel} aria-label="Close">
            <IconClose size={18} />
          </button>
        </header>

        <div className="source-form-body">
          <label className="form-row">
            <span className="form-label">Kind</span>
            <select value={kind} onChange={(event) => changeKind(event.target.value)} disabled={!!initial}>
              {kinds.map((item) => (
                <option key={item.kind} value={item.kind}>
                  {item.title}
                </option>
              ))}
            </select>
            {descriptor?.description && <span className="form-help">{descriptor.description}</span>}
          </label>

          <div className="form-grid">
            <label className="form-row">
              <span className="form-label">Label</span>
              <input value={label} onChange={(event) => setLabel(event.target.value)} placeholder="BBC World" />
            </label>
            <label className="form-row">
              <span className="form-label">Group</span>
              <input value={group} onChange={(event) => setGroup(event.target.value)} placeholder="World" />
            </label>
          </div>

          <div className="form-grid">
            <label className="form-row">
              <span className="form-label">Poll every</span>
              <select value={intervalSec} onChange={(event) => setIntervalSec(Number(event.target.value))}>
                {intervalChoices.map((value) => (
                  <option key={value} value={value}>
                    {value < 3600 ? `${value / 60} min` : `${value / 3600} h`}
                  </option>
                ))}
              </select>
            </label>
            <label className="form-row">
              <span className="form-label">Site</span>
              <input value={site} onChange={(event) => setSite(event.target.value)} placeholder="https://example.com" />
            </label>
          </div>

          {descriptor?.fields.map((field) => (
            <label className="form-row" key={field.key}>
              <span className="form-label">
                {field.label}
                {field.required && <b className="form-required">*</b>}
              </span>
              {field.type === 'select' ? (
                <select
                  value={fieldText(field, options[field.key])}
                  onChange={(event) => setOption(field.key, event.target.value)}
                >
                  {(field.options ?? []).map((option) => (
                    <option key={option} value={option}>
                      {option === '' ? 'Use global proxy' : option}
                    </option>
                  ))}
                </select>
              ) : field.type === 'bool' ? (
                <span className="form-check">
                  <input
                    type="checkbox"
                    checked={Boolean(options[field.key])}
                    onChange={(event) => setOption(field.key, event.target.checked)}
                  />
                  <span>{field.help ?? 'Enabled'}</span>
                </span>
              ) : field.type === 'number' ? (
                <input
                  type="number"
                  min={1}
                  value={fieldText(field, options[field.key])}
                  onChange={(event) => setOption(field.key, event.target.value)}
                />
              ) : field.type === 'secret' ? (
                <input
                  type="password"
                  value={fieldText(field, options[field.key])}
                  onChange={(event) => setOption(field.key, event.target.value)}
                  autoComplete="new-password"
                />
              ) : (
                <input
                  value={fieldText(field, options[field.key])}
                  onChange={(event) => setOption(field.key, event.target.value)}
                  placeholder={field.type === 'list' ? 'comma-separated' : undefined}
                />
              )}
              {field.help && field.type !== 'bool' && <span className="form-help">{field.help}</span>}
            </label>
          ))}

          <label className="form-check form-enabled">
            <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
            <span>Start polling immediately</span>
          </label>

          {probe && (
            <div className={probe.ok ? 'probe ok' : 'probe failed'}>
              <div className="probe-head">
                {probe.ok ? `${probe.count} items fetched` : 'The source did not answer'}
              </div>
              {probe.ok ? (
                <ul>
                  {(probe.titles ?? []).map((title) => (
                    <li key={title}>{title}</li>
                  ))}
                </ul>
              ) : (
                <p>{probe.error}</p>
              )}
            </div>
          )}
          {error && <div className="form-error">{error}</div>}
        </div>

        <footer className="source-form-foot">
          <button type="button" className="text-btn" onClick={test} disabled={testing || saving}>
            {testing ? 'Testing…' : 'Test source'}
          </button>
          <div className="source-form-actions">
            <button type="button" className="ghost-btn" onClick={onCancel} disabled={saving}>
              Cancel
            </button>
            <button type="submit" className="primary-btn" disabled={saving}>
              {saving ? 'Saving…' : initial ? 'Save changes' : 'Add source'}
            </button>
          </div>
        </footer>
      </form>
    </div>
  )
}
