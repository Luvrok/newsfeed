import { useEffect, useMemo, useRef, useState } from 'react'
import type { Story } from '../api'
import type { IdSet } from '../hooks'
import {
  formatClock,
  formatFull,
  groupByDay,
  isFresh,
  kindColor,
  plural,
  storyMeta,
} from '../lib'
import type { Tab } from '../types'
import { IconArrow, IconBookmark } from './Icons'

interface StoryRowProps {
  story: Story
  index: number
  now: Date
  read: boolean
  saved: boolean
  focused: boolean
  lead: boolean
  onOpen: (story: Story) => void
  onToggleSave: (story: Story) => void
}

function StoryRow({
  story,
  index,
  now,
  read,
  saved,
  focused,
  lead,
  onOpen,
  onToggleSave,
}: StoryRowProps) {
  const [broken, setBroken] = useState(false)
  useEffect(() => setBroken(false), [story.image])

  const image = story.image && !broken ? story.image : ''
  const tags = storyMeta(story)
  const fresh = isFresh(story.published_at, now.getTime(), 25)
  const className = [
    'story',
    lead ? 'lead' : '',
    image ? 'has-thumb' : '',
    read ? 'read' : '',
    focused ? 'focused' : '',
  ]
    .filter(Boolean)
    .join(' ')

  const meta = (
    <div className="story-meta">
      <span className="kind-dot" style={{ background: kindColor(story.kind) }} />
      <span className="story-source">{story.source}</span>
      {tags.map((tag, tagIndex) => (
        <span className="meta-tag" key={`${tag}-${tagIndex}`}>
          <span className="meta-sep">·</span>
          {tag}
        </span>
      ))}
      {fresh && <span className="new-flag">new</span>}
    </div>
  )

  if (lead) {
    return (
      <article className={className} data-story-index={index}>
        <a
          className="story-hit"
          href={story.url}
          onClick={(event) => {
            event.preventDefault()
            onOpen(story)
          }}
          aria-label={story.title}
        />
        <div className="lead-copy">
          <div className="lead-kicker">
            <span>Latest</span>
            <time dateTime={story.published_at} title={formatFull(story.published_at)}>
              {formatClock(story.published_at)}
            </time>
          </div>
          {meta}
          <h3 className="story-title">{story.title}</h3>
          {story.summary && <p className="story-summary">{story.summary}</p>}
          <div className="lead-foot">
            <span className="lead-open">
              Read story <IconArrow size={13} />
            </span>
            {story.author && <span className="lead-author">by {story.author}</span>}
            <button
              className={saved ? 'story-save saved' : 'story-save'}
              onClick={(event) => {
                event.stopPropagation()
                onToggleSave(story)
              }}
              aria-label={saved ? 'Remove from saved' : 'Save story'}
              aria-pressed={saved}
              title={saved ? 'Remove from saved' : 'Save for later'}
            >
              <IconBookmark size={16} filled={saved} />
            </button>
          </div>
        </div>
        {image && (
          <div className="story-thumb lead-thumb">
            <img src={image} alt="" loading="lazy" decoding="async" onError={() => setBroken(true)} />
          </div>
        )}
      </article>
    )
  }

  return (
    <article className={className} data-story-index={index}>
      <a
        className="story-hit"
        href={story.url}
        onClick={(event) => {
          event.preventDefault()
          onOpen(story)
        }}
        aria-label={story.title}
      />
      <time className="story-time" dateTime={story.published_at} title={formatFull(story.published_at)}>
        {formatClock(story.published_at)}
      </time>
      <div className="story-content">
        {meta}
        <h3 className="story-title">{story.title}</h3>
        {story.summary && <p className="story-summary">{story.summary}</p>}
      </div>
      {image && (
        <div className="story-thumb">
          <img src={image} alt="" loading="lazy" decoding="async" onError={() => setBroken(true)} />
        </div>
      )}
      <div className="story-action">
        <button
          className={saved ? 'story-save saved' : 'story-save'}
          onClick={(event) => {
            event.stopPropagation()
            onToggleSave(story)
          }}
          aria-label={saved ? 'Remove from saved' : 'Save story'}
          aria-pressed={saved}
          title={saved ? 'Remove from saved' : 'Save for later'}
        >
          <IconBookmark size={15} filled={saved} />
        </button>
      </div>
    </article>
  )
}

interface StoryListProps {
  items: Story[]
  now: Date
  read: IdSet
  saved: IdSet
  onOpen: (story: Story) => void
  onToggleSave: (story: Story) => void
  focus: number
  showLead: boolean
  loading: boolean
  loadingMore: boolean
  hasMore: boolean
  error: string | null
  tab: Tab
  onLoadMore: () => void
  onRetry: () => void
  onReset: () => void
}

export function StoryList({
  items,
  now,
  read,
  saved,
  onOpen,
  onToggleSave,
  focus,
  showLead,
  loading,
  loadingMore,
  hasMore,
  error,
  tab,
  onLoadMore,
  onRetry,
  onReset,
}: StoryListProps) {
  const sentinel = useRef<HTMLDivElement | null>(null)
  const groups = useMemo(() => groupByDay(items, now), [items, now])
  const positions = useMemo(() => {
    const map = new Map<number, number>()
    items.forEach((item, index) => map.set(item.id, index))
    return map
  }, [items])

  useEffect(() => {
    const el = sentinel.current
    if (!el || loading || !hasMore) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) onLoadMore()
      },
      { rootMargin: '700px 0px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [onLoadMore, loading, hasMore])

  if (loading) {
    return (
      <div className="feed-list" aria-busy="true">
        <div className="skeleton-day" />
        {Array.from({ length: 6 }, (_, index) => (
          <div className="skeleton-row" key={index}>
            <div className="skeleton skeleton-time" />
            <div className="skeleton-copy">
              <div className="skeleton skeleton-meta" />
              <div className="skeleton skeleton-title" style={{ width: `${86 - index * 4}%` }} />
              <div className="skeleton skeleton-summary" style={{ width: `${64 - index * 3}%` }} />
            </div>
            <div className="skeleton skeleton-thumb" />
          </div>
        ))}
      </div>
    )
  }

  if (error && items.length === 0) {
    return (
      <div className="feed-error">
        <div className="error-mark">!</div>
        <div>
          <b>The wire is quiet</b>
          <p>{error}</p>
          <button className="text-btn" onClick={onRetry}>
            Try again
          </button>
        </div>
      </div>
    )
  }

  if (items.length === 0) {
    const hint =
      tab === 'saved'
        ? 'Nothing saved yet. Use the bookmark on a story to keep it for later.'
        : tab === 'unread'
          ? 'Everything loaded so far has been read. Pull in more pages or refresh the sources.'
          : 'No stories match these filters.'
    return (
      <div className="feed-empty">
        <div className="empty-rule" />
        <b>Nothing here yet</b>
        <p>{hint}</p>
        <button className="text-btn" onClick={onReset}>
          Reset filters
        </button>
      </div>
    )
  }

  return (
    <div className="feed-list">
      {error && (
        <div className="feed-banner">
          <span>{error}</span>
          <button className="text-btn" onClick={onRetry}>
            Retry
          </button>
        </div>
      )}
      {groups.map((group) => (
        <section className="day-group" key={group.key}>
          <h2 className="day">
            <span className="day-label">{group.label}</span>
            <span className="day-count">
              {group.items.length} {plural(group.items.length, 'story', 'stories')}
            </span>
            <i className="day-rule" />
          </h2>
          {group.items.map((story) => {
            const index = positions.get(story.id) ?? 0
            return (
              <StoryRow
                key={story.id}
                story={story}
                index={index}
                now={now}
                read={read.has(story.id)}
                saved={saved.has(story.id)}
                focused={focus === index}
                lead={showLead && index === 0}
                onOpen={onOpen}
                onToggleSave={onToggleSave}
              />
            )
          })}
        </section>
      ))}

      <div className="feed-sentinel" ref={sentinel} />
      {hasMore ? (
        <div className="feed-more">
          <button className="more-btn" onClick={onLoadMore} disabled={loadingMore}>
            {loadingMore ? <span className="spinner" /> : null}
            {loadingMore ? 'Loading the wire…' : 'Load more stories'}
          </button>
        </div>
      ) : (
        <div className="feed-end">
          <span className="end-mark">◆</span>
          <span>
            {tab === 'saved'
              ? `${items.length} saved ${plural(items.length, 'story', 'stories')}`
              : tab === 'unread'
                ? `${items.length} unread in the loaded window`
                : `That's the whole wire, ${items.length} ${plural(items.length, 'story', 'stories')} loaded.`}
          </span>
        </div>
      )}
    </div>
  )
}
