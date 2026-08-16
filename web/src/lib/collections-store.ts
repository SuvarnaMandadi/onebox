// Shared, single-flight cache for GET /api/collections — every page that
// needs the collections list (Dashboard, Collections, Collection Detail)
// reads from here instead of each firing its own fetch, so navigating
// between them doesn't re-request data that's already in hand. Call
// refresh() after any mutation (create/delete/schema update) to
// invalidate.
import { useSyncExternalStore, useCallback, useEffect } from "react"
import { api, ApiError } from "@/lib/api"
import type { Collection } from "@/lib/types"

interface State {
  collections: Collection[] | null
  error: string | null
  loading: boolean
}

let state: State = { collections: null, error: null, loading: false }
let inflight: Promise<void> | null = null
const listeners = new Set<() => void>()

function setState(patch: Partial<State>) {
  state = { ...state, ...patch }
  listeners.forEach((l) => l())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function getSnapshot() {
  return state
}

async function load(): Promise<void> {
  if (inflight) return inflight
  setState({ loading: true, error: null })
  inflight = api
    .get<{ items: Collection[] | null }>("/api/collections")
    .then((res) => {
      setState({ collections: res.items ?? [], loading: false })
    })
    .catch((err) => {
      setState({ error: err instanceof ApiError ? err.message : "Failed to load collections.", loading: false })
    })
    .finally(() => {
      inflight = null
    })
  return inflight
}

export function useCollections() {
  const snapshot = useSyncExternalStore(subscribe, getSnapshot)

  useEffect(() => {
    if (state.collections === null && !state.loading && !state.error) {
      void load()
    }
  }, [])

  const refresh = useCallback(() => load(), [])

  return { ...snapshot, refresh }
}

/** Blanks the cache (so consumers show loading state again) and refetches — the command palette's "Clear Cache" vs. the softer "Refresh Data" (which just re-runs load() and keeps stale data on screen until the new response lands). */
export function clearCollectionsCache() {
  setState({ collections: null, error: null })
  void load()
}
