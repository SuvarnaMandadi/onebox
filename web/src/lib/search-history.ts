// Recent/pinned search queries typed into the command palette — purely
// local (there's no backend concept of "search history"), same honesty
// rule as every other local-metadata store in this app (collection-meta.ts,
// file-meta.ts, ai-conversations.ts).
import { useSyncExternalStore } from "react"

export interface SearchHistoryEntry {
  id: string
  query: string
  pinned: boolean
  lastUsedAt: number
}

const STORAGE_KEY = "onebox_search_history"
const MAX_ENTRIES = 20

function read(): SearchHistoryEntry[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return raw ? (JSON.parse(raw) as SearchHistoryEntry[]) : []
  } catch {
    return []
  }
}

let cache: SearchHistoryEntry[] = read()
const listeners = new Set<() => void>()

function write(next: SearchHistoryEntry[]) {
  cache = next
  localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  listeners.forEach((l) => l())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** Records a search the admin actually acted on (selected a result / pressed Enter) — not every keystroke. */
export function recordSearch(query: string) {
  const trimmed = query.trim()
  if (!trimmed) return
  const existing = cache.find((e) => e.query.toLowerCase() === trimmed.toLowerCase())
  const rest = cache.filter((e) => e !== existing)
  const entry: SearchHistoryEntry = existing
    ? { ...existing, lastUsedAt: Date.now() }
    : { id: `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, query: trimmed, pinned: false, lastUsedAt: Date.now() }
  const next = [entry, ...rest]
  const pinned = next.filter((e) => e.pinned)
  const unpinned = next.filter((e) => !e.pinned).slice(0, MAX_ENTRIES)
  write([...pinned, ...unpinned])
}

export function togglePinnedSearch(id: string) {
  write(cache.map((e) => (e.id === id ? { ...e, pinned: !e.pinned } : e)))
}

export function removeSearchHistoryEntry(id: string) {
  write(cache.filter((e) => e.id !== id))
}

/** Called on logout — local search history has no per-user namespacing,
 * so on a shared machine the next person to log in would otherwise see
 * whatever the previous account searched for. */
export function clearSearchHistory() {
  write([])
}

export function useSearchHistory(): SearchHistoryEntry[] {
  return useSyncExternalStore(subscribe, () => cache)
}
