// Client-side-only file organization (tags, color, favorite, pinned,
// last-opened) — fileRecord (internal/server/files.go) has no such
// columns and this milestone doesn't add any (see Milestone 2's "keep the
// backend unchanged" constraint). Same pattern and same honesty rule as
// collection-meta.ts: this is per-browser localStorage state, never
// presented as if the server returned it.
import { useSyncExternalStore } from "react"

export interface FileMeta {
  tags: string[]
  color: string | null
  favorite: boolean
  pinned: boolean
  lastOpenedAt?: number
  /** Times markFileOpened has fired — "frequently used" for the AI Workspace's Workspace Memory panel. */
  openCount?: number
}

const STORAGE_KEY = "onebox_file_meta"
const DEFAULT_META: FileMeta = { tags: [], color: null, favorite: false, pinned: false }

type Store = Record<string, FileMeta>

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

export function getFileMeta(id: string): FileMeta {
  return { ...DEFAULT_META, ...cache[id] }
}

export function setFileMeta(id: string, patch: Partial<FileMeta>) {
  write({ ...cache, [id]: { ...getFileMeta(id), ...patch } })
}

export function toggleFileFavorite(id: string) {
  setFileMeta(id, { favorite: !getFileMeta(id).favorite })
}

export function toggleFilePinned(id: string) {
  setFileMeta(id, { pinned: !getFileMeta(id).pinned })
}

export function markFileOpened(id: string) {
  setFileMeta(id, { lastOpenedAt: Date.now(), openCount: (getFileMeta(id).openCount ?? 0) + 1 })
}

export function frequentlyUsedFiles(ids: string[], limit = 5): string[] {
  return ids
    .filter((id) => (cache[id]?.openCount ?? 0) > 0)
    .sort((a, b) => (cache[b].openCount ?? 0) - (cache[a].openCount ?? 0))
    .slice(0, limit)
}

export function removeFileMeta(id: string) {
  const next = { ...cache }
  delete next[id]
  write(next)
}

export function useFileMetaStore(): Store {
  return useSyncExternalStore(subscribe, () => cache)
}

export function recentlyOpenedFiles(ids: string[], limit = 8): string[] {
  return ids
    .filter((id) => cache[id]?.lastOpenedAt)
    .sort((a, b) => (cache[b].lastOpenedAt ?? 0) - (cache[a].lastOpenedAt ?? 0))
    .slice(0, limit)
}
