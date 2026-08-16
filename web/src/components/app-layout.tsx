import { Outlet, useLocation } from "react-router-dom"
import { AppSidebar, navItems } from "@/components/app-sidebar"
import { SiteHeader } from "@/components/site-header"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { CommandPalette } from "@/components/command-palette/command-palette"

export function AppLayout() {
  const { pathname } = useLocation()
  const active = navItems.find((item) =>
    item.end ? pathname === item.to : pathname.startsWith(item.to) && item.to !== "/",
  )

  return (
    <SidebarProvider>
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:bg-background focus:px-3 focus:py-2 focus:text-sm focus:shadow"
      >
        Skip to content
      </a>
      <AppSidebar />
      <SidebarInset>
        <SiteHeader title={active?.label ?? "OneBox"} />
        <main id="main-content" tabIndex={-1} className="flex-1 overflow-auto p-6 focus:outline-none">
          <Outlet />
        </main>
      </SidebarInset>
      <CommandPalette />
    </SidebarProvider>
  )
}
