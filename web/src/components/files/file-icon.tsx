import {
  Archive,
  File as FileIconLucide,
  FileCode,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileVideo,
  Music,
  type LucideIcon,
} from "lucide-react"
import { cn } from "@/lib/utils"
import { colorClasses } from "@/lib/collection-icons"
import { fileCategory } from "@/lib/file-render"

const CATEGORY_ICON: Record<string, LucideIcon> = {
  image: FileImage,
  pdf: FileText,
  video: FileVideo,
  audio: Music,
  spreadsheet: FileSpreadsheet,
  document: FileText,
  archive: Archive,
  code: FileCode,
  text: FileText,
  other: FileIconLucide,
}

const CATEGORY_COLOR: Record<string, string> = {
  image: "violet",
  pdf: "red",
  video: "pink",
  audio: "teal",
  spreadsheet: "green",
  document: "blue",
  archive: "amber",
  code: "indigo",
  text: "gray",
  other: "gray",
}

export function FileIcon({
  mime,
  filename,
  color,
  className,
  iconClassName,
}: {
  mime: string
  filename: string
  color?: string | null
  className?: string
  iconClassName?: string
}) {
  const category = fileCategory(mime, filename)
  const Icon = CATEGORY_ICON[category] ?? FileIconLucide
  return (
    <div className={cn("flex size-9 shrink-0 items-center justify-center rounded-md", colorClasses(color || CATEGORY_COLOR[category]), className)}>
      <Icon className={cn("size-4.5", iconClassName)} />
    </div>
  )
}
