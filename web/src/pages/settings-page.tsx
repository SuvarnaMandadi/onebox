import { Settings } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"

export function SettingsPage() {
  return (
    <Empty className="h-full">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <Settings />
        </EmptyMedia>
        <EmptyTitle>Settings is coming to the new dashboard</EmptyTitle>
        <EmptyDescription>
          Provider keys, chat model, and account settings are still on the classic dashboard for now — this page
          migrates in an upcoming milestone.
        </EmptyDescription>
      </EmptyHeader>
      <Button asChild size="sm">
        <a href="/_/#/settings">Open classic settings</a>
      </Button>
    </Empty>
  )
}
