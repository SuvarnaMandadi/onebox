import { Component, type ErrorInfo, type ReactNode } from "react"
import { Button } from "@/components/ui/button"

// React only supports catching render errors with a class component — there's
// no hook equivalent. This sits once at the top of the route tree (see
// App.tsx) because the whole SPA ships as a single go:embed'd bundle: a tab
// left open across a deploy still has the *old* chunk-hash URLs baked into
// its already-loaded JS, so navigating to a route it hasn't fetched yet
// throws a dynamic-import error instead of rendering. Without this boundary
// that error unmounts the entire tree to a blank white screen.
function isChunkLoadError(message: string) {
  return (
    message.includes("Failed to fetch dynamically imported module") ||
    message.includes("error loading dynamically imported module")
  )
}

interface State {
  error: Error | null
}

export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Uncaught render error:", error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children

    const stale = isChunkLoadError(error.message)
    return (
      <div className="flex h-screen w-full flex-col items-center justify-center gap-3 p-6 text-center">
        <p className="text-sm font-medium">
          {stale ? "A new version is available — reload to continue" : "Something went wrong"}
        </p>
        <Button size="sm" onClick={() => window.location.reload()}>
          Reload
        </Button>
      </div>
    )
  }
}
