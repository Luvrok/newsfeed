import { useEffect, useRef, useState } from 'react'
import { getStory, type SourcesResponse, type Story } from '../api'
import type { IdSet } from '../hooks'
import { formatFull, formatRelative, kindColor, kindLabel } from '../lib'
import type { Theme } from '../types'
import {
  BrandMark,
  IconArrow,
  IconArrowLeft,
  IconBookmark,
  IconMoon,
  IconSun,
} from './Icons'

interface StoryPageProps {
  id: number
  theme: Theme
  onToggleTheme: () => void
  onBack: () => void
  stats: SourcesResponse | null
  onStatus: () => void
  saved: IdSet
  onToggleSave: (story: Story) => void
  onRead: (id: number) => void
}

export function StoryPage({
  id,
  theme,
  onToggleTheme,
  onBack,
  stats,
  onStatus,
  saved,
  onToggleSave,
  onRead,
}: StoryPageProps) {
  const [story, setStory] = useState<Story | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [imageBroken, setImageBroken] = useState(false)
  const [heroReady, setHeroReady] = useState(false)
  const [progress, setProgress] = useState(0)
  const readRef = useRef<number | null>(null)
  const bodyRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    setStory(null)
    setImageBroken(false)
    setHeroReady(false)
    getStory(id, controller.signal)
      .then((res) => {
        setStory(res.item)
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        setError(err instanceof Error ? err.message : 'Could not load this story')
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [id])

  useEffect(() => {
    if (!story || readRef.current === story.id) return
    readRef.current = story.id
    onRead(story.id)
  }, [story, onRead])

  useEffect(() => {
    if (!story) return
    const previous = document.title
    document.title = `${story.title} — Dispatch`
    return () => {
      document.title = previous
    }
  }, [story])

  useEffect(() => {
    const body = bodyRef.current
    if (!body) return
    const images = Array.from(body.querySelectorAll<HTMLImageElement>('img'))
    if (images[0]) images[0].loading = 'eager'
    const cleanups: Array<() => void> = []
    images.forEach((image) => {
      image.classList.add('loading')
      const ready = () => {
        image.classList.remove('loading')
        image.classList.add('ready')
      }
      const hide = () => {
        image.classList.remove('loading')
        image.style.display = 'none'
      }
      if (image.complete && image.naturalWidth > 0) ready()
      else image.addEventListener('load', ready)
      image.addEventListener('error', hide)
      cleanups.push(() => {
        image.removeEventListener('load', ready)
        image.removeEventListener('error', hide)
      })
    })
    return () => cleanups.forEach((cleanup) => cleanup())
  }, [story])

  useEffect(() => {
    const onScroll = () => {
      const max = document.documentElement.scrollHeight - window.innerHeight
      setProgress(max > 0 ? Math.min(1, Math.max(0, window.scrollY / max)) : 0)
    }
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
        return
      }
      if (event.key === 'Escape' || event.code === 'KeyB') {
        event.preventDefault()
        onBack()
      }
      if ((event.code === 'KeyS' || event.key === 's') && story) {
        event.preventDefault()
        onToggleSave(story)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onBack, onToggleSave, story])

  const online = stats ? stats.sources.filter((s) => s.status === 'ok').length : 0
  const total = stats?.sources.length ?? 0
  const isSaved = story ? saved.has(story.id) : false

  const rawContent = story?.content?.trim() ?? ''
  const content = rawContent === story?.title.trim() ? '' : rawContent
  const hasContent = content.length > 0
  const hasEarlyImage = /<img\b/i.test(content.slice(0, 600))
  const hero = story?.image && !imageBroken && !hasEarlyImage ? story.image : ''

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
            <span className="live-text">
              {total ? `${online}/${total}` : '—'}
            </span>
            <span className="live-label">live</span>
          </button>
          <button className="icon-btn" onClick={onToggleTheme} aria-label="Toggle theme">
            {theme === 'dark' ? <IconSun size={17} /> : <IconMoon size={17} />}
          </button>
        </div>
      </header>
      <div className="reading-progress" aria-hidden="true">
        <i style={{ width: `${progress * 100}%` }} />
      </div>

      <main className="article-page">
        {loading && (
          <article className="article">
            <div className="skeleton skeleton-meta" style={{ width: 180, height: 10 }} />
            <div className="skeleton article-skeleton-title" />
            <div className="skeleton article-skeleton-title short" />
            <div className="skeleton article-skeleton-line" />
            <div className="skeleton article-skeleton-line" />
            <div className="skeleton article-skeleton-line short" />
          </article>
        )}

        {!loading && error && (
          <div className="feed-error article-error">
            <div className="error-mark">!</div>
            <div>
              <b>This story is not on the wire</b>
              <p>{error}</p>
              <button className="text-btn" onClick={onBack}>
                Back to the feed
              </button>
            </div>
          </div>
        )}

        {!loading && !error && story && (
          <article className="article">
            <button className="back-link" onClick={onBack}>
              <IconArrowLeft size={15} />
              <span>Back to the wire</span>
            </button>

            <div className="article-meta">
              <span className="kind-dot" style={{ background: kindColor(story.kind) }} />
              <span className="article-source">{story.source}</span>
              <span className="meta-sep">·</span>
              <span>{kindLabel(story.kind)}</span>
              {story.author && story.author !== story.source && (
                <>
                  <span className="meta-sep">·</span>
                  <span>{story.author}</span>
                </>
              )}
              <span className="meta-sep">·</span>
              <time dateTime={story.published_at} title={formatFull(story.published_at)}>
                {formatFull(story.published_at)}
              </time>
            </div>

            <h1 className={story.title.length > 90 ? 'article-title long' : 'article-title'}>
              {story.title}
            </h1>

            {!hasContent && story.summary && <p className="article-lede">{story.summary}</p>}

            {hero && (
              <figure className={heroReady ? 'article-hero ready' : 'article-hero loading'}>
                {!heroReady && <span className="thumb-loader" />}
                <img
                  src={hero}
                  alt=""
                  decoding="async"
                  onLoad={() => setHeroReady(true)}
                  onError={() => setImageBroken(true)}
                />
              </figure>
            )}

            <div className="article-toolbar">
              <span className="article-stamp">{relativeStamp(story.published_at)}</span>
              <div className="article-actions">
                <button
                  className={isSaved ? 'article-action saved' : 'article-action'}
                  onClick={() => onToggleSave(story)}
                  aria-pressed={isSaved}
                >
                  <IconBookmark size={15} filled={isSaved} />
                  {isSaved ? 'Saved' : 'Save'}
                </button>
                <a
                  className="article-action original"
                  href={story.url}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  Open original
                  <IconArrow size={14} />
                </a>
              </div>
            </div>

            {hasContent ? (
              story.content_format === 'text' ? (
                <div className="article-body text">{content}</div>
              ) : (
                <div
                  ref={bodyRef}
                  className="article-body"
                  dangerouslySetInnerHTML={{ __html: content }}
                />
              )
            ) : story.kind === 'telegram' ? null : (
              <div className="article-note">
                <span className="article-note-mark">◇</span>
                <div>
                  <b>The source only publishes a short excerpt.</b>
                  <p>
                    Dispatch stores what the feed or API delivered, so nothing breaks when the
                    publisher redesigns a page. The rest of this story is at {story.source}.
                  </p>
                  <a href={story.url} target="_blank" rel="noopener noreferrer">
                    Read the full story <IconArrow size={13} />
                  </a>
                </div>
              </div>
            )}

            <footer className="article-footer">
              <div className="article-tags">
                {story.tags.map((tag, index) => (
                  <span key={`${tag}-${index}`}>{tag}</span>
                ))}
              </div>
              <a
                className="article-original"
                href={story.url}
                target="_blank"
                rel="noopener noreferrer"
              >
                Read the full story at {story.source}
                <IconArrow size={14} />
              </a>
              <button className="back-link footer-back" onClick={onBack}>
                <IconArrowLeft size={15} />
                <span>Back to the wire</span>
              </button>
            </footer>
          </article>
        )}
      </main>
    </>
  )
}


function relativeStamp(iso: string): string {
  const value = formatRelative(iso)
  return value === 'now' ? 'just now' : `${value} ago`
}
