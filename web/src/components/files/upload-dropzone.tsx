import { useEffect, useRef, useState, type ReactNode } from "react"
import { UploadCloud } from "lucide-react"
import { enqueueUploads } from "@/lib/upload-manager"
import { cn } from "@/lib/utils"

// Wraps page content as a drop target (drop anywhere inside to upload).
// This alone is NOT the accessible entry point — drag-and-drop has no
// keyboard/screen-reader equivalent — so every page using this also
// renders an UploadButton (below) as the real, focusable, labeled way to
// pick files; the dropzone is a bonus for pointer users on top of that,
// never the only way in.
export function UploadDropzone({ children, className }: { children: ReactNode; className?: string }) {
  const [dragOver, setDragOver] = useState(false)
  const dragCounter = useRef(0)

  function onFilesPicked(fileList: FileList | null) {
    if (!fileList || fileList.length === 0) return
    enqueueUploads(Array.from(fileList))
  }

  return (
    <div
      className={cn("relative", className)}
      onDragEnter={(e) => {
        e.preventDefault()
        dragCounter.current++
        setDragOver(true)
      }}
      onDragLeave={(e) => {
        e.preventDefault()
        dragCounter.current--
        if (dragCounter.current <= 0) setDragOver(false)
      }}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault()
        dragCounter.current = 0
        setDragOver(false)
        onFilesPicked(e.dataTransfer.files)
      }}
    >
      {children}
      {dragOver && (
        <div className="pointer-events-none fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm">
          <div className="flex flex-col items-center gap-2 rounded-xl border-2 border-dashed border-primary bg-card p-10 shadow-lg">
            <UploadCloud className="size-8 text-primary" />
            <p className="font-medium">Drop to upload</p>
          </div>
        </div>
      )}
    </div>
  )
}

// UploadButton is the non-drag fallback used in the page toolbar — its
// own independent file input (not the dropzone's), so it works standalone
// anywhere on the page, not just inside an UploadDropzone. autoOpen lets
// the command palette's "Upload File" action pop the native file picker
// immediately after navigating here (fires once, on mount).
export function UploadButton({ children, autoOpen }: { children: (openPicker: () => void) => ReactNode; autoOpen?: boolean }) {
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (autoOpen) inputRef.current?.click()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return (
    <>
      <input
        ref={inputRef}
        type="file"
        multiple
        className="sr-only"
        aria-label="Upload files"
        onChange={(e) => {
          if (e.target.files?.length) enqueueUploads(Array.from(e.target.files))
          e.target.value = ""
        }}
      />
      {children(() => inputRef.current?.click())}
    </>
  )
}
