import { FileQuestion } from "lucide-react"
import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"

export function NotFoundPage() {
  return (
    <Empty className="h-full min-h-screen">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <FileQuestion />
        </EmptyMedia>
        <EmptyTitle>Page not found</EmptyTitle>
        <EmptyDescription>That URL doesn't match anything in OneBox.</EmptyDescription>
      </EmptyHeader>
      <Button asChild size="sm">
        <Link to="/">Back to dashboard</Link>
      </Button>
    </Empty>
  )
}
