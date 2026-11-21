import { useEffect } from 'react'
import { IconClose } from './Icons'

interface ShortcutsProps {
  open: boolean
  onClose: () => void
}

const KEYS: Array<[string, string]> = [
  ['j / k', 'Move down / up'],
  ['Enter or o', 'Open the focused story'],
  ['s', 'Save or unsave the focused story'],
  ['/', 'Focus search'],
  ['r', 'Refresh all sources'],
  ['?', 'Toggle this panel'],
  ['Esc', 'Close panels, clear focus'],
  ['⌘ / Ctrl + K', 'Focus search from anywhere'],
]

export function Shortcuts({ open, onClose }: ShortcutsProps) {
  useEffect(() => {
    if (!open) return
    const handler = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="overlay" onClick={onClose}>
      <div className="sheet" role="dialog" aria-modal="true" aria-label="Keyboard shortcuts" onClick={(e) => e.stopPropagation()}>
        <div className="sheet-head">
          <span className="rail-label">Keyboard</span>
          <button className="icon-btn" onClick={onClose} aria-label="Close">
            <IconClose size={17} />
          </button>
        </div>
        <div className="keys">
          {KEYS.map(([key, label]) => (
            <div className="key-row" key={key}>
              <kbd>{key}</kbd>
              <span>{label}</span>
            </div>
          ))}
        </div>
        <p className="sheet-foot">The list follows the keyboard focus. Stories open in a new tab.</p>
      </div>
    </div>
  )
}
