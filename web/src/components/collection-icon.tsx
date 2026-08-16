import { cn } from "@/lib/utils"
import { colorClasses, iconFor } from "@/lib/collection-icons"

export function CollectionIcon({
  icon,
  color,
  className,
  iconClassName,
}: {
  icon: string
  color: string
  className?: string
  iconClassName?: string
}) {
  const Icon = iconFor(icon)
  return (
    <div className={cn("flex size-9 shrink-0 items-center justify-center rounded-md", colorClasses(color), className)}>
      <Icon className={cn("size-4.5", iconClassName)} />
    </div>
  )
}
