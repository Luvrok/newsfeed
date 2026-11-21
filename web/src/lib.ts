import type { Story } from './api'

export const KIND_META: Record<string, { label: string; color: string }> = {
  rss: { label: 'RSS', color: '#6ea8fe' },
  telegram: { label: 'Telegram', color: '#58b8e8' },
  hackernews: { label: 'Hacker News', color: '#ff7a45' },
  lobsters: { label: 'Lobsters', color: '#e15a5a' },
  mastodon: { label: 'Mastodon', color: '#a78bfa' },
  usgs: { label: 'USGS', color: '#e8b44a' },
  github: { label: 'GitHub', color: '#9aa0a6' },
  gdelt: { label: 'GDELT', color: '#4ec9b0' },
}

export function kindColor(kind: string): string {
  return KIND_META[kind]?.color ?? '#8b929c'
}

export function kindLabel(kind: string): string {
  return KIND_META[kind]?.label ?? kind
}

export const GROUP_ORDER = ['world', 'tech', 'russia', 'social', 'official']

export function groupLabel(group: string): string {
  switch (group) {
    case 'world':
      return 'World'
    case 'tech':
      return 'Technology'
    case 'russia':
      return 'Russia & CIS'
    case 'social':
      return 'Social'
    case 'official':
      return 'Official'
    case 'feeds':
      return 'Feeds'
    default:
      return group ? group.charAt(0).toUpperCase() + group.slice(1) : 'Other'
  }
}

const clock = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
const weekday = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long' })
const fullDate = new Intl.DateTimeFormat(undefined, {
  weekday: 'short',
  day: 'numeric',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
})

export function formatClock(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return clock.format(d)
}

export function formatFull(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return fullDate.format(d)
}

export function formatRelative(iso: string, now: number = Date.now()): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const diff = Math.max(0, now - t)
  const sec = Math.floor(diff / 1000)
  if (sec < 45) return 'now'
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m`
  const hours = Math.floor(min / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  const weeks = Math.floor(days / 7)
  return `${weeks}w`
}

export function isFresh(iso: string, now: number = Date.now(), minutes = 20): boolean {
  const t = new Date(iso).getTime()
  return !Number.isNaN(t) && now - t < minutes * 60_000 && now - t >= -60_000
}

export function dayStart(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
}

export function dayLabel(iso: string, now: Date): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const today = dayStart(now)
  const day = dayStart(d)
  const diff = Math.round((today - day) / 86_400_000)
  if (diff === 0) return 'Today'
  if (diff === 1) return 'Yesterday'
  return weekday.format(d)
}

export interface DayGroup {
  key: string
  label: string
  items: Story[]
}

export function groupByDay(items: Story[], now: Date): DayGroup[] {
  const groups: DayGroup[] = []
  const index = new Map<string, DayGroup>()
  for (const item of items) {
    const d = new Date(item.published_at)
    if (Number.isNaN(d.getTime())) continue
    const key = `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`
    let group = index.get(key)
    if (!group) {
      group = { key, label: dayLabel(item.published_at, now), items: [] }
      index.set(key, group)
      groups.push(group)
    }
    group.items.push(item)
  }
  return groups
}

export function dedupe(items: Story[]): Story[] {
  const seen = new Set<number>()
  const out: Story[] = []
  for (const item of items) {
    if (seen.has(item.id)) continue
    seen.add(item.id)
    out.push(item)
  }
  return out
}

export function sortStories(items: Story[]): Story[] {
  return [...items].sort((a, b) => {
    const ta = new Date(a.published_at).getTime()
    const tb = new Date(b.published_at).getTime()
    if (tb !== ta) return tb - ta
    return b.id - a.id
  })
}

export function storyMeta(story: Story): string[] {
  const out: string[] = []
  const source = story.source.toLowerCase()
  if (story.author && story.author.toLowerCase() !== source) out.push(story.author)
  for (const tag of story.tags) {
    if (out.length >= 2) break
    const value = tag.toLowerCase()
    if (!value || value === source || value === story.author.toLowerCase()) continue
    if (value.includes(source) || source.includes(value)) continue
    out.push(tag)
  }
  return out
}

export function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many
}

export function compactNumber(n: number): string {
  if (n < 1000) return String(n)
  if (n < 10_000) return `${(n / 1000).toFixed(1).replace(/\.0$/, '')}k`
  return `${Math.round(n / 1000)}k`
}
