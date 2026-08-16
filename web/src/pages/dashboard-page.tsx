import { Link } from "react-router-dom"
import { Database, Layers, Plus, Sparkles } from "lucide-react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { useCollections } from "@/lib/collections-store"

export function DashboardPage() {
  const { collections, error } = useCollections()

  const totalRecords = collections?.reduce((sum, c) => sum + c.record_count, 0) ?? 0

  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-3">
        <StatCard icon={Database} label="Collections" value={collections?.length} />
        <StatCard icon={Layers} label="Total records" value={collections ? totalRecords : undefined} />
        <Card>
          <CardContent className="flex items-center justify-between p-4">
            <div>
              <p className="text-sm text-muted-foreground">AI Workspace</p>
              <p className="text-sm font-medium">Ask the copilot anything</p>
            </div>
            <Button asChild size="icon" variant="secondary">
              <Link to="/ai" aria-label="Open AI Workspace">
                <Sparkles />
              </Link>
            </Button>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <CardTitle>Collections</CardTitle>
          <Button asChild size="sm" variant="outline">
            <Link to="/collections">
              <Plus />
              New collection
            </Link>
          </Button>
        </CardHeader>
        <CardContent>
          {error && <p className="text-sm text-destructive">{error}</p>}
          {!error && collections === null && (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          )}
          {collections !== null && collections.length === 0 && (
            <Empty>
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Database />
                </EmptyMedia>
                <EmptyTitle>No collections yet</EmptyTitle>
                <EmptyDescription>
                  Create your first collection, or ask the AI copilot to design one for you.
                </EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <div className="flex gap-2">
                  <Button asChild size="sm">
                    <Link to="/collections">Create collection</Link>
                  </Button>
                  <Button asChild size="sm" variant="outline">
                    <Link to="/ai">Ask the copilot</Link>
                  </Button>
                </div>
              </EmptyContent>
            </Empty>
          )}
          {collections !== null && collections.length > 0 && (
            <ul className="divide-y">
              {collections.map((c) => (
                <li key={c.id}>
                  <Link
                    to={`/collections/${c.name}`}
                    className="flex items-center justify-between rounded-md px-2 py-3 text-sm transition-colors hover:bg-accent"
                  >
                    <span className="font-medium">{c.name}</span>
                    <span className="text-muted-foreground">
                      {c.schema.fields.length} field{c.schema.fields.length === 1 ? "" : "s"} ·{" "}
                      {c.record_count} record{c.record_count === 1 ? "" : "s"}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function StatCard({
  icon: Icon,
  label,
  value,
}: {
  icon: React.ComponentType<{ className?: string }>
  label: string
  value: number | undefined
}) {
  return (
    <Card>
      <CardContent className="flex items-center gap-3 p-4">
        <div className="flex size-9 items-center justify-center rounded-md bg-muted">
          <Icon className="size-4" />
        </div>
        <div>
          <p className="text-sm text-muted-foreground">{label}</p>
          {value === undefined ? (
            <Skeleton className="mt-1 h-5 w-10" />
          ) : (
            <p className="text-lg font-semibold leading-none">{value}</p>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
