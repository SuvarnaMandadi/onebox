// Collection "meta" (icon, color, description, favorite, last-opened) that
// the backend has no concept of — `collection` (internal/server/collections.go)
// is just {id, name, schema, rules, record_count, created, updated}. Rather
// than extend the backend schema for cosmetic dashboard-only state (out of
// scope for this milestone — see the task's "keep the backend unchanged"
// constraint), this is kept client-side in localStorage, keyed by
// collection name. It's genuinely local/per-browser, same category of
// state as the old dashboard's own localStorage usage (chat history,
// share-link cache) — nothing here is presented as if it came from the API.
import { useSyncExternalStore } from "react"

export interface CollectionMeta {
  icon: string
  color: string
  description: string
  favorite: boolean
  lastOpenedAt?: number
  /** Times markOpened has fired — "frequently used" for the AI Workspace's Workspace Memory panel, local like everything else here. */
  openCount?: number
}

const STORAGE_KEY = "onebox_collection_meta"

export const DEFAULT_ICON = "Database"
export const DEFAULT_COLOR = "gray"

export const COLLECTION_COLORS = [
  "gray", "red", "orange", "amber", "green", "teal", "blue", "indigo", "violet", "pink",
] as const

const DEFAULT_META: CollectionMeta = {
  icon: DEFAULT_ICON,
  color: DEFAULT_COLOR,
  description: "",
  favorite: false,
}

type Store = Record<string, CollectionMeta>

function read(): Store {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return raw ? (JSON.parse(raw) as Store) : {}
  } catch {
    return {}
  }
}

let cache: Store = read()
const listeners = new Set<() => void>()

function write(next: Store) {
  cache = next
  localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  listeners.forEach((l) => l())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getMeta(name: string): CollectionMeta {
  return { ...DEFAULT_META, ...cache[name] }
}

export function setMeta(name: string, patch: Partial<CollectionMeta>) {
  write({ ...cache, [name]: { ...getMeta(name), ...patch } })
}

export function toggleFavorite(name: string) {
  setMeta(name, { favorite: !getMeta(name).favorite })
}

export function markOpened(name: string) {
  setMeta(name, { lastOpenedAt: Date.now(), openCount: (getMeta(name).openCount ?? 0) + 1 })
}

export function frequentlyUsed(names: string[], limit = 5): string[] {
  return names
    .filter((n) => (cache[n]?.openCount ?? 0) > 0)
    .sort((a, b) => (cache[b].openCount ?? 0) - (cache[a].openCount ?? 0))
    .slice(0, limit)
}

export function removeMeta(name: string) {
  const next = { ...cache }
  delete next[name]
  write(next)
}

export function renameMeta(oldName: string, newName: string) {
  const next = { ...cache }
  if (next[oldName]) {
    next[newName] = next[oldName]
    delete next[oldName]
    write(next)
  }
}

/** Subscribes a component to the whole meta store, re-rendering on any change. */
export function useCollectionMetaStore(): Store {
  return useSyncExternalStore(subscribe, () => cache)
}

export function recentlyOpened(names: string[], limit = 5): string[] {
  return names
    .filter((n) => cache[n]?.lastOpenedAt)
    .sort((a, b) => (cache[b].lastOpenedAt ?? 0) - (cache[a].lastOpenedAt ?? 0))
    .slice(0, limit)
}
