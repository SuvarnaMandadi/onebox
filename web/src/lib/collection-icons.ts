// Curated icon set for collection.icon (a plain string name, stored via
// collection-meta.ts) — small and fixed rather than exposing all ~1500
// lucide icons, same "constrained palette reads as designed, not default"
// principle the dataviz/color guidance uses elsewhere in this app.
import {
  Database, Users, ShoppingCart, FileText, Calendar, Mail, MessageSquare,
  Image, Package, Tag, Star, Bell, Briefcase, Book, Camera, Clipboard,
  CreditCard, Heart, Home, Map, Music, Phone, Truck, Wallet,
  type LucideIcon,
} from "lucide-react"

export const COLLECTION_ICONS: Record<string, LucideIcon> = {
  Database, Users, ShoppingCart, FileText, Calendar, Mail, MessageSquare,
  Image, Package, Tag, Star, Bell, Briefcase, Book, Camera, Clipboard,
  CreditCard, Heart, Home, Map, Music, Phone, Truck, Wallet,
}

export function iconFor(name: string): LucideIcon {
  return COLLECTION_ICONS[name] ?? Database
}

// Tailwind class pairs per color name — background + foreground, light and
// dark handled by Tailwind's own dark: variant already active app-wide.
export const COLOR_CLASSES: Record<string, string> = {
  gray: "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300",
  red: "bg-red-100 text-red-600 dark:bg-red-950 dark:text-red-400",
  orange: "bg-orange-100 text-orange-600 dark:bg-orange-950 dark:text-orange-400",
  amber: "bg-amber-100 text-amber-600 dark:bg-amber-950 dark:text-amber-400",
  green: "bg-green-100 text-green-600 dark:bg-green-950 dark:text-green-400",
  teal: "bg-teal-100 text-teal-600 dark:bg-teal-950 dark:text-teal-400",
  blue: "bg-blue-100 text-blue-600 dark:bg-blue-950 dark:text-blue-400",
  indigo: "bg-indigo-100 text-indigo-600 dark:bg-indigo-950 dark:text-indigo-400",
  violet: "bg-violet-100 text-violet-600 dark:bg-violet-950 dark:text-violet-400",
  pink: "bg-pink-100 text-pink-600 dark:bg-pink-950 dark:text-pink-400",
}

export function colorClasses(color: string): string {
  return COLOR_CLASSES[color] ?? COLOR_CLASSES.gray
}

// Left-border accent per color — used where a color should read as a
// card-level accent stripe ("similar to Chrome profile colors") rather
// than recoloring an icon that already carries real meaning (a file
// type's icon, in particular, shouldn't change based on an organizational
// color choice — see files-page.tsx's FileCard).
export const ACCENT_BORDER_CLASSES: Record<string, string> = {
  gray: "border-l-gray-300 dark:border-l-gray-600",
  red: "border-l-red-500",
  orange: "border-l-orange-500",
  amber: "border-l-amber-500",
  green: "border-l-green-500",
  teal: "border-l-teal-500",
  blue: "border-l-blue-500",
  indigo: "border-l-indigo-500",
  violet: "border-l-violet-500",
  pink: "border-l-pink-500",
}

export function accentBorderClass(color: string | null | undefined): string {
  return ACCENT_BORDER_CLASSES[color ?? "gray"] ?? ACCENT_BORDER_CLASSES.gray
}
