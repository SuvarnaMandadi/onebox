import { lazy, Suspense } from "react"
import { Navigate, Route, Routes } from "react-router-dom"
import { useAuth } from "@/lib/auth"
import { AppLayout } from "@/components/app-layout"
import { ErrorBoundary } from "@/components/error-boundary"
import { LoginPage } from "@/pages/login-page"
import { NotFoundPage } from "@/pages/not-found-page"

// Route-level code splitting: everything past the login gate is its own
// chunk, so the first paint (the login screen, or a redirect straight to
// it) never pays for Collections/AI-workspace/Files/Settings code it
// isn't using yet.
const DashboardPage = lazy(() => import("@/pages/dashboard-page").then((m) => ({ default: m.DashboardPage })))
const CollectionsPage = lazy(() => import("@/pages/collections-page").then((m) => ({ default: m.CollectionsPage })))
const CollectionDetailPage = lazy(() =>
  import("@/pages/collection-detail-page").then((m) => ({ default: m.CollectionDetailPage })),
)
const RecordsPage = lazy(() => import("@/pages/records-page").then((m) => ({ default: m.RecordsPage })))
const AIWorkspacePage = lazy(() => import("@/pages/ai-workspace-page").then((m) => ({ default: m.AIWorkspacePage })))
const FilesPage = lazy(() => import("@/pages/files-page").then((m) => ({ default: m.FilesPage })))
const SettingsPage = lazy(() => import("@/pages/settings-page").then((m) => ({ default: m.SettingsPage })))

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { isAuthenticated } = useAuth()
  if (!isAuthenticated) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <ErrorBoundary>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          element={
            <RequireAuth>
              <AppLayout />
            </RequireAuth>
          }
        >
          <Route
            path="/"
            element={
              <Suspense fallback={null}>
                <DashboardPage />
              </Suspense>
            }
          />
          <Route
            path="/ai"
            element={
              <Suspense fallback={null}>
                <AIWorkspacePage />
              </Suspense>
            }
          />
          <Route
            path="/collections"
            element={
              <Suspense fallback={null}>
                <CollectionsPage />
              </Suspense>
            }
          />
          <Route
            path="/collections/:name"
            element={
              <Suspense fallback={null}>
                <CollectionDetailPage />
              </Suspense>
            }
          />
          <Route
            path="/collections/:name/records"
            element={
              <Suspense fallback={null}>
                <RecordsPage />
              </Suspense>
            }
          />
          <Route
            path="/files"
            element={
              <Suspense fallback={null}>
                <FilesPage />
              </Suspense>
            }
          />
          <Route
            path="/settings"
            element={
              <Suspense fallback={null}>
                <SettingsPage />
              </Suspense>
            }
          />
        </Route>
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </ErrorBoundary>
  )
}
