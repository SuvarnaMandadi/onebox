// Shared, single-flight cache for GET /api/files — same rationale as
// collections-store.ts: avoid every mount of the Files page (or a future
// file-picker elsewhere) re-fetching what's already loaded. Holds one
// growing page set (cursor-paginated) rather than the whole table, since
// unlike collections there's no fixed small N here.
import { useSyncExternalStore, useCallback, useEffect } from "react"
import { api, ApiError } from "@/lib/api"
import type { FileRecord, FilesListResponse } from "@/lib/types"

interface State {
  files: FileRecord[] | null
  nextCursor: string | undefined
  total: number
  error: string | null
  loading: boolean
  loadingMore: boolean
}

let state: State = { files: null, nextCursor: undefined, total: 0, error: null, loading: false, loadingMore: false }
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

async function load(): Promise<void> {
  if (inflight) return inflight
  setState({ loading: true, error: null })
  inflight = api
    .get<FilesListResponse>("/api/files?limit=100")
    .then((res) => setState({ files: res.items, nextCursor: res.nextCursor, total: res.total, loading: false }))
    .catch((err) => setState({ error: err instanceof ApiError ? err.message : "Failed to load files.", loading: false }))
    .finally(() => {
      inflight = null
    })
  return inflight
}

async function loadMore(): Promise<void> {
  if (!state.nextCursor || state.loadingMore) return
  setState({ loadingMore: true })
  try {
    const res = await api.get<FilesListResponse>(`/api/files?limit=100&cursor=${encodeURIComponent(state.nextCursor)}`)
    setState({ files: [...(state.files ?? []), ...res.items], nextCursor: res.nextCursor, total: res.total })
  } catch (err) {
    setState({ error: err instanceof ApiError ? err.message : "Failed to load more files." })
  } finally {
    setState({ loadingMore: false })
  }
}

export function addFilesToStore(files: FileRecord[]) {
  setState({ files: [...files, ...(state.files ?? [])], total: state.total + files.length })
}

export function removeFilesFromStore(ids: string[]) {
  setState({
    files: state.files?.filter((f) => !ids.includes(f.id)) ?? null,
    total: Math.max(0, state.total - ids.length),
  })
}

export function useFiles() {
  const snapshot = useSyncExternalStore(subscribe, () => state)

  useEffect(() => {
    if (state.files === null && !state.loading && !state.error) {
      void load()
    }
  }, [])

  const refresh = useCallback(() => load(), [])
  const fetchMore = useCallback(() => loadMore(), [])

  return { ...snapshot, refresh, fetchMore }
}

/** See clearCollectionsCache's doc comment — same "blank then refetch" behavior for GET /api/files. */
export function clearFilesCache() {
  setState({ files: null, nextCursor: undefined, error: null })
  void load()
}
