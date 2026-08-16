// Upload queue backing the drag-and-drop / multi-file upload experience.
// Uses XMLHttpRequest (not fetch) specifically because it's the only
// browser API that exposes upload progress events — needed for real
// per-file progress bars, not a simulated/fake one.
import { useSyncExternalStore } from "react"
import { toast } from "sonner"
import { getToken } from "@/lib/api"
import { addFilesToStore } from "@/lib/files-store"
import type { FileRecord } from "@/lib/types"

export type UploadStatus = "queued" | "uploading" | "done" | "error" | "canceled"

export interface UploadItem {
  id: string
  file: File
  status: UploadStatus
  progress: number
  error?: string
  xhr?: XMLHttpRequest
}

let items: UploadItem[] = []
const listeners = new Set<() => void>()

function notify() {
  listeners.forEach((l) => l())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function patch(id: string, p: Partial<UploadItem>) {
  items = items.map((it) => (it.id === id ? { ...it, ...p } : it))
  notify()
}

function runUpload(item: UploadItem, onSettled?: () => void) {
  const xhr = new XMLHttpRequest()
  patch(item.id, { status: "uploading", progress: 0, xhr, error: undefined })

  xhr.upload.addEventListener("progress", (e) => {
    if (e.lengthComputable) patch(item.id, { progress: Math.round((e.loaded / e.total) * 100) })
  })
  xhr.addEventListener("load", () => {
    if (xhr.status >= 200 && xhr.status < 300) {
      patch(item.id, { status: "done", progress: 100 })
      try {
        const rec = JSON.parse(xhr.responseText) as FileRecord
        addFilesToStore([rec])
      } catch {
        // response wasn't the expected shape — upload still succeeded
        // server-side, so leave status "done" and let the next list
        // refresh pick it up rather than erroring a successful upload.
      }
    } else {
      let message = `Upload failed (${xhr.status})`
      try {
        const body = JSON.parse(xhr.responseText)
        if (body?.message) message = body.message
      } catch {
        /* non-JSON error body */
      }
      patch(item.id, { status: "error", error: message })
    }
    onSettled?.()
  })
  xhr.addEventListener("error", () => {
    patch(item.id, { status: "error", error: "Network error" })
    onSettled?.()
  })
  xhr.addEventListener("abort", () => {
    patch(item.id, { status: "canceled" })
    onSettled?.()
  })

  const form = new FormData()
  form.append("file", item.file)
  xhr.open("POST", "/api/files")
  const token = getToken()
  if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`)
  xhr.send(form)
}

// Dropping a large batch used to fire every file's XHR simultaneously,
// competing for bandwidth/connections instead of queueing. Uploads
// started via enqueueUploads are now capped at MAX_CONCURRENT_UPLOADS in
// flight at once; items beyond the cap sit in "queued" status until a
// slot frees up. Manual retryUpload (a single, deliberate user action)
// deliberately bypasses this queue and always runs immediately, same as
// before.
const MAX_CONCURRENT_UPLOADS = 3
let activeUploads = 0

function fillUploadSlots() {
  while (activeUploads < MAX_CONCURRENT_UPLOADS) {
    const next = items.find((it) => it.status === "queued")
    if (!next) return
    activeUploads++
    runUpload(next, () => {
      activeUploads--
      fillUploadSlots()
    })
  }
}

export function enqueueUploads(files: File[]) {
  const newItems: UploadItem[] = files.map((file) => ({
    id: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
    file,
    status: "queued",
    progress: 0,
  }))
  items = [...newItems, ...items]
  notify()
  fillUploadSlots()
  if (files.length > 1) toast.info(`Uploading ${files.length} files…`)
}

export function cancelUpload(id: string) {
  const item = items.find((it) => it.id === id)
  if (!item) return
  if (item.xhr) {
    // Already sent — aborting fires the "abort" listener in runUpload,
    // which patches status to "canceled" and (if this item held a queue
    // slot) frees it via onSettled.
    item.xhr.abort()
    return
  }
  // Still sitting in "queued", waiting for a slot from fillUploadSlots —
  // there's no in-flight request to abort yet, so without this branch
  // canceling a still-queued item was a silent no-op: it stayed "queued"
  // and would still start uploading whenever a slot opened up, directly
  // contradicting the user's cancel action.
  patch(id, { status: "canceled" })
}

export function retryUpload(id: string) {
  const item = items.find((it) => it.id === id)
  if (item) runUpload(item)
}

export function dismissUpload(id: string) {
  items = items.filter((it) => it.id !== id)
  notify()
}

export function clearFinishedUploads() {
  items = items.filter((it) => it.status === "queued" || it.status === "uploading")
  notify()
}

export function useUploadQueue(): UploadItem[] {
  return useSyncExternalStore(subscribe, () => items)
}
