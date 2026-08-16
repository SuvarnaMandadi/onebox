import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Label } from "@/components/ui/label"
import { CollectionIcon } from "@/components/collection-icon"
import { COLLECTION_ICONS, colorClasses } from "@/lib/collection-icons"
import { COLLECTION_COLORS } from "@/lib/collection-meta"
import { cn } from "@/lib/utils"

export function IconColorPicker({
  icon,
  color,
  onIconChange,
  onColorChange,
}: {
  icon: string
  color: string
  onIconChange: (icon: string) => void
  onColorChange: (color: string) => void
}) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button type="button" className="mt-6" aria-label="Choose icon and color">
          <CollectionIcon icon={icon} color={color} className="size-11 transition-opacity hover:opacity-80" />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-64 space-y-3" align="start">
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">Icon</Label>
          <div className="grid grid-cols-6 gap-1">
            {Object.entries(COLLECTION_ICONS).map(([name, Icon]) => (
              <button
                key={name}
                type="button"
                onClick={() => onIconChange(name)}
                className={cn(
                  "flex size-8 items-center justify-center rounded-md hover:bg-accent",
                  icon === name && "bg-accent ring-1 ring-ring",
                )}
                aria-label={name}
              >
                <Icon className="size-4" />
              </button>
            ))}
          </div>
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs text-muted-foreground">Color</Label>
          <div className="flex flex-wrap gap-1.5">
            {COLLECTION_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => onColorChange(c)}
                className={cn(
                  "size-6 rounded-full",
                  colorClasses(c).split(" ")[0],
                  color === c && "ring-2 ring-ring ring-offset-2 ring-offset-popover",
                )}
                aria-label={c}
              />
            ))}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
