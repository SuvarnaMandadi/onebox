export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B"
  const units = ["B", "KB", "MB", "GB"]
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** i
  return `${i === 0 ? value : value.toFixed(1)} ${units[i]}`
}

export function extOf(filename: string): string {
  const i = filename.lastIndexOf(".")
  return i === -1 ? "" : filename.slice(i).toLowerCase()
}

export type PreviewKind = "image" | "pdf" | "markdown" | "text" | "json" | "csv" | "none"

const TEXTY_EXT: Record<string, PreviewKind> = {
  ".md": "markdown",
  ".markdown": "markdown",
  ".txt": "text",
  ".json": "json",
  ".csv": "csv",
}

export function previewKind(mime: string, filename: string): PreviewKind {
  if (mime.startsWith("image/")) return "image"
  if (mime === "application/pdf") return "pdf"
  const ext = extOf(filename)
  if (TEXTY_EXT[ext]) return TEXTY_EXT[ext]
  if (mime.startsWith("text/")) return "text"
  if (mime === "application/json") return "json"
  return "none"
}

// Mirrors chatAttachmentExtensions exactly (internal/server/chat_attachments.go)
// — the real, enforced allowlist for what "Ask AI about this file" can
// actually do anything with (image sent to a vision-capable model, or
// document text extracted into the prompt). Kept in sync by hand since
// it's a small, stable list; the honesty requirement is that this NEVER
// claims a wider set than the backend actually supports.
const AI_READABLE_EXT = new Set([".png", ".jpg", ".jpeg", ".pdf", ".docx", ".txt", ".md", ".csv", ".xlsx"])

export function isAiReadable(filename: string): boolean {
  return AI_READABLE_EXT.has(extOf(filename))
}

export function fileCategory(mime: string, filename: string): string {
  const ext = extOf(filename)
  if (mime.startsWith("image/")) return "image"
  if (mime === "application/pdf") return "pdf"
  if (mime.startsWith("video/")) return "video"
  if (mime.startsWith("audio/")) return "audio"
  if ([".csv", ".xlsx", ".xls"].includes(ext)) return "spreadsheet"
  if ([".doc", ".docx"].includes(ext)) return "document"
  if ([".zip", ".tar", ".gz", ".rar", ".7z"].includes(ext)) return "archive"
  if (mime === "application/json" || ext === ".json") return "code"
  if (mime.startsWith("text/") || [".md", ".txt"].includes(ext)) return "text"
  return "other"
}
