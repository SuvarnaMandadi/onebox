// Open/close state for the command palette, lifted out of the component
// itself so other UI (the header's search button, a future "Focus Search"
// trigger) can open it without prop-drilling — same external-store
// pattern used throughout this app (collections-store.ts, etc.).
import { useSyncExternalStore } from "react"

let open = false
const listeners = new Set<() => void>()

function set(next: boolean) {
  open = next
  listeners.forEach((l) => l())
}

export function openCommandPalette() {
  set(true)
}

export function closeCommandPalette() {
  set(false)
}

export function toggleCommandPalette() {
  set(!open)
}

export function useCommandPaletteOpen(): [boolean, (open: boolean) => void] {
  const value = useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    () => open,
  )
  return [value, set]
}
