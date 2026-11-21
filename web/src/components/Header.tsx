import type { RefObject } from 'react'
import {
  BrandMark,
  IconClose,
  IconKeyboard,
  IconMenu,
  IconMoon,
  IconRefresh,
  IconSearch,
  IconSun,
} from './Icons'

interface HeaderProps {
  search: string
  onSearch: (value: string) => void
  searchRef: RefObject<HTMLInputElement | null>
  theme: 'dark' | 'light'
  onToggleTheme: () => void
  onRefresh: () => void
  refreshing: boolean
  sourcesOk: number
  sourcesTotal: number
  onMenu: () => void
  onShortcuts: () => void
  onHome: () => void
}

export function Header({
  search,
  onSearch,
  searchRef,
  theme,
  onToggleTheme,
  onRefresh,
  refreshing,
  sourcesOk,
  sourcesTotal,
  onMenu,
  onShortcuts,
  onHome,
}: HeaderProps) {
  return (
    <header className={refreshing ? 'topbar refreshing' : 'topbar'}>
      <div className="topbar-left">
        <button className="icon-btn menu-btn" onClick={onMenu} aria-label="Open sources">
          <IconMenu size={19} />
        </button>
        <button className="brand" onClick={onHome} aria-label="Dispatch, back to the front page">
          <BrandMark size={25} />
          <span className="brand-name">
            Dispatch<span className="brand-dot">.</span>
          </span>
        </button>
      </div>

      <div className="search">
        <IconSearch className="search-icon" size={16} />
        <input
          ref={searchRef}
          value={search}
          onChange={(event) => onSearch(event.target.value)}
          placeholder="Search the wire…"
          aria-label="Search stories"
          autoComplete="off"
          spellCheck={false}
        />
        {search ? (
          <button className="search-clear" onClick={() => onSearch('')} aria-label="Clear search">
            <IconClose size={14} />
          </button>
        ) : (
          <kbd className="search-kbd">⌘K</kbd>
        )}
      </div>

      <div className="topbar-right">
        <div className="live-chip" title={`${sourcesOk} of ${sourcesTotal} sources healthy`}>
          <span className={sourcesOk === sourcesTotal ? 'live-dot' : 'live-dot warn'} />
          <span className="live-text">
            {sourcesOk}/{sourcesTotal}
          </span>
          <span className="live-label">live</span>
        </div>
        <button
          className={refreshing ? 'icon-btn spin' : 'icon-btn'}
          onClick={onRefresh}
          disabled={refreshing}
          aria-label="Refresh sources"
          title="Refresh all sources"
        >
          <IconRefresh size={17} />
        </button>
        <button className="icon-btn shortcuts-btn" onClick={onShortcuts} aria-label="Keyboard shortcuts">
          <IconKeyboard size={18} />
        </button>
        <button className="icon-btn" onClick={onToggleTheme} aria-label="Toggle theme">
          {theme === 'dark' ? <IconSun size={17} /> : <IconMoon size={17} />}
        </button>
      </div>
      <div className="topbar-progress" aria-hidden="true" />
    </header>
  )
}
