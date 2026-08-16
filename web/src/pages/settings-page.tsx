import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { AlertTriangle, CheckCircle2, Loader2, Play, Save, ShieldCheck } from "lucide-react"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Progress } from "@/components/ui/progress"
import { api, ApiError } from "@/lib/api"
import {
  getBackendHealth,
  getDiagnosticsHistory,
  getPerformanceSummary,
  runDiagnostics,
  streamOllamaPull,
  validateSettings,
} from "@/lib/diagnostics-api"
import type {
  BackendHealthReport,
  ConfigIssue,
  DiagnosticsHistoryEntry,
  OllamaPullProgress,
  PerformanceSummary,
  ProviderDiagnosticsReport,
} from "@/lib/diagnostics-types"
import { ProviderDiagnosticsPanel } from "@/components/settings/provider-diagnostics-panel"
import { BackendHealthPanel } from "@/components/settings/backend-health-panel"
import { ConnectionHistoryPanel, PerformancePanel } from "@/components/settings/connection-history-panel"

// -- settings load/save -------------------------------------------------

interface SecretFlag {
  set: boolean
}
type SettingsBlob = Record<string, string | SecretFlag>

function isSecretSet(v: string | SecretFlag | undefined): boolean {
  return typeof v === "object" && v !== null && "set" in v && v.set
}
function asString(v: string | SecretFlag | undefined): string {
  return typeof v === "string" ? v : ""
}

type ChatProvider = "ollama" | "anthropic" | "openai"
type EmbeddingProvider = "openai" | "ollama" | "voyage"

export function SettingsPage() {
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)

  const [chatProvider, setChatProvider] = useState<ChatProvider>("ollama")
  const [chatModel, setChatModel] = useState("")
  const [anthropicKeySet, setAnthropicKeySet] = useState(false)
  const [anthropicKeyInput, setAnthropicKeyInput] = useState("")
  const [openaiKeySet, setOpenaiKeySet] = useState(false)
  const [openaiKeyInput, setOpenaiKeyInput] = useState("")
  const [openaiBaseURL, setOpenaiBaseURL] = useState("")
  const [ollamaBaseURL, setOllamaBaseURL] = useState("")

  const [embeddingProvider, setEmbeddingProvider] = useState<EmbeddingProvider>("openai")
  const [embeddingModel, setEmbeddingModel] = useState("")
  const [embeddingKeySet, setEmbeddingKeySet] = useState(false)
  const [embeddingKeyInput, setEmbeddingKeyInput] = useState("")
  const [embeddingBaseURL, setEmbeddingBaseURL] = useState("")

  const [issues, setIssues] = useState<ConfigIssue[]>([])

  useEffect(() => {
    api.get<SettingsBlob>("/api/settings").then((data) => {
      setChatProvider((asString(data.chat_provider) as ChatProvider) || "ollama")
      setChatModel(asString(data.chat_model))
      setAnthropicKeySet(isSecretSet(data.anthropic_api_key))
      setOpenaiKeySet(isSecretSet(data.openai_api_key))
      setOpenaiBaseURL(asString(data.openai_base_url))
      setOllamaBaseURL(asString(data.ollama_base_url))
      setEmbeddingProvider((asString(data.embedding_provider) as EmbeddingProvider) || "openai")
      setEmbeddingModel(asString(data.embedding_model))
      setEmbeddingKeySet(isSecretSet(data.embedding_api_key))
      setEmbeddingBaseURL(asString(data.embedding_base_url))
      setLoaded(true)
    })
  }, [])

  // Section 8: validate on every change, debounced — never wait for Save.
  const validateTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const runValidation = useCallback(
    (candidate: Record<string, string>) => {
      if (validateTimer.current) clearTimeout(validateTimer.current)
      validateTimer.current = setTimeout(() => {
        validateSettings(candidate)
          .then((res) => setIssues(res.issues))
          .catch(() => {})
      }, 400)
    },
    [],
  )
  useEffect(() => {
    if (!loaded) return
    runValidation({
      chat_provider: chatProvider,
      chat_model: chatModel,
      ollama_base_url: ollamaBaseURL,
      openai_base_url: openaiBaseURL,
      embedding_provider: embeddingProvider,
      embedding_model: embeddingModel,
      embedding_base_url: embeddingBaseURL,
    })
    // Deliberately excludes API key inputs — those are write-only fields
    // (see isSecretSet) and their presence/absence is already reflected
    // via anthropicKeySet/openaiKeySet/embeddingKeySet in the merged
    // server-side state the validate endpoint reads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded, chatProvider, chatModel, ollamaBaseURL, openaiBaseURL, embeddingProvider, embeddingModel, embeddingBaseURL])

  async function handleSave() {
    setSaving(true)
    try {
      const body: Record<string, string> = {
        chat_provider: chatProvider,
        chat_model: chatModel,
        openai_base_url: openaiBaseURL,
        ollama_base_url: ollamaBaseURL,
        embedding_provider: embeddingProvider,
        embedding_model: embeddingModel,
        embedding_base_url: embeddingBaseURL,
      }
      if (anthropicKeyInput) body.anthropic_api_key = anthropicKeyInput
      if (openaiKeyInput) body.openai_api_key = openaiKeyInput
      if (embeddingKeyInput) body.embedding_api_key = embeddingKeyInput

      await api.put("/api/settings", body)
      if (anthropicKeyInput) setAnthropicKeySet(true)
      if (openaiKeyInput) setOpenaiKeySet(true)
      if (embeddingKeyInput) setEmbeddingKeySet(true)
      setAnthropicKeyInput("")
      setOpenaiKeyInput("")
      setEmbeddingKeyInput("")
      toast.success("Settings saved")
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Failed to save settings")
    } finally {
      setSaving(false)
    }
  }

  if (!loaded) {
    return <p className="text-sm text-muted-foreground">Loading settings…</p>
  }

  return (
    <div className="space-y-6 pb-10">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Settings</h1>
        <p className="text-sm text-muted-foreground">Configure providers and monitor the health of this OneBox instance.</p>
      </div>

      {!!issues.length && (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/5 p-3">
          <div className="mb-1 flex items-center gap-1.5 text-sm font-medium text-amber-700 dark:text-amber-400">
            <AlertTriangle className="size-4" /> {issues.length} configuration issue{issues.length === 1 ? "" : "s"}
          </div>
          <ul className="list-disc space-y-0.5 pl-5 text-xs text-muted-foreground">
            {issues.map((issue, i) => (
              <li key={i}>{issue.message}</li>
            ))}
          </ul>
        </div>
      )}

      <div id="provider-config-form" className="grid gap-4 lg:grid-cols-2">
        <ChatProviderCard
          provider={chatProvider}
          onProvider={setChatProvider}
          model={chatModel}
          onModel={setChatModel}
          anthropicKeySet={anthropicKeySet}
          anthropicKeyInput={anthropicKeyInput}
          onAnthropicKeyInput={setAnthropicKeyInput}
          openaiKeySet={openaiKeySet}
          openaiKeyInput={openaiKeyInput}
          onOpenaiKeyInput={setOpenaiKeyInput}
          openaiBaseURL={openaiBaseURL}
          onOpenaiBaseURL={setOpenaiBaseURL}
          ollamaBaseURL={ollamaBaseURL}
          onOllamaBaseURL={setOllamaBaseURL}
        />
        <EmbeddingProviderCard
          provider={embeddingProvider}
          onProvider={setEmbeddingProvider}
          model={embeddingModel}
          onModel={setEmbeddingModel}
          keySet={embeddingKeySet}
          keyInput={embeddingKeyInput}
          onKeyInput={setEmbeddingKeyInput}
          baseURL={embeddingBaseURL}
          onBaseURL={setEmbeddingBaseURL}
          ollamaBaseURL={ollamaBaseURL}
        />
      </div>

      <div className="flex justify-end">
        <Button onClick={handleSave} disabled={saving}>
          {saving ? <Loader2 className="animate-spin" /> : <Save />}
          Save settings
        </Button>
      </div>

      <Tabs defaultValue="chat">
        <TabsList>
          <TabsTrigger value="chat">Chat provider</TabsTrigger>
          <TabsTrigger value="embedding">Embedding provider</TabsTrigger>
          <TabsTrigger value="health">Backend health</TabsTrigger>
        </TabsList>
        <TabsContent value="chat">
          <DiagnosticsSection
            requestProvider={chatProvider}
            baseURL={chatProvider === "ollama" ? ollamaBaseURL : chatProvider === "openai" ? openaiBaseURL : undefined}
            model={chatModel}
          />
        </TabsContent>
        <TabsContent value="embedding">
          <DiagnosticsSection requestProvider="embedding" baseURL={embeddingBaseURL} embeddingModel={embeddingModel} />
        </TabsContent>
        <TabsContent value="health">
          <BackendHealthSection />
        </TabsContent>
      </Tabs>
    </div>
  )
}

// -- Chat / Embedding provider config cards --------------------------------

function ChatProviderCard(props: {
  provider: ChatProvider
  onProvider: (v: ChatProvider) => void
  model: string
  onModel: (v: string) => void
  anthropicKeySet: boolean
  anthropicKeyInput: string
  onAnthropicKeyInput: (v: string) => void
  openaiKeySet: boolean
  openaiKeyInput: string
  onOpenaiKeyInput: (v: string) => void
  openaiBaseURL: string
  onOpenaiBaseURL: (v: string) => void
  ollamaBaseURL: string
  onOllamaBaseURL: (v: string) => void
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Chat provider</CardTitle>
        <CardDescription>Powers the AI Workspace and admin chatbot.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="space-y-1.5">
          <Label>Provider</Label>
          <Select value={props.provider} onValueChange={(v) => props.onProvider(v as ChatProvider)}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="ollama">Ollama (local)</SelectItem>
              <SelectItem value="anthropic">Anthropic</SelectItem>
              <SelectItem value="openai">OpenAI</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {props.provider === "ollama" && (
          <div className="space-y-1.5">
            <Label>Ollama base URL</Label>
            <Input placeholder="http://localhost:11434" value={props.ollamaBaseURL} onChange={(e) => props.onOllamaBaseURL(e.target.value)} />
          </div>
        )}
        {props.provider === "openai" && (
          <>
            <div className="space-y-1.5">
              <Label>OpenAI base URL</Label>
              <Input placeholder="https://api.openai.com/v1" value={props.openaiBaseURL} onChange={(e) => props.onOpenaiBaseURL(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label>API key {props.openaiKeySet && <span className="text-xs text-muted-foreground">(set — leave blank to keep)</span>}</Label>
              <Input type="password" placeholder={props.openaiKeySet ? "••••••••" : "sk-..."} value={props.openaiKeyInput} onChange={(e) => props.onOpenaiKeyInput(e.target.value)} />
            </div>
          </>
        )}
        {props.provider === "anthropic" && (
          <div className="space-y-1.5">
            <Label>API key {props.anthropicKeySet && <span className="text-xs text-muted-foreground">(set — leave blank to keep)</span>}</Label>
            <Input type="password" placeholder={props.anthropicKeySet ? "••••••••" : "sk-ant-..."} value={props.anthropicKeyInput} onChange={(e) => props.onAnthropicKeyInput(e.target.value)} />
          </div>
        )}

        <div className="space-y-1.5">
          <Label>Model</Label>
          <Input placeholder={props.provider === "ollama" ? "llama3.2:3b" : props.provider === "anthropic" ? "claude-sonnet-5" : "gpt-4o-mini"} value={props.model} onChange={(e) => props.onModel(e.target.value)} />
        </div>
      </CardContent>
    </Card>
  )
}

function EmbeddingProviderCard(props: {
  provider: EmbeddingProvider
  onProvider: (v: EmbeddingProvider) => void
  model: string
  onModel: (v: string) => void
  keySet: boolean
  keyInput: string
  onKeyInput: (v: string) => void
  baseURL: string
  onBaseURL: (v: string) => void
  ollamaBaseURL: string
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Embedding provider</CardTitle>
        <CardDescription>Powers RAG document ingestion and search.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="space-y-1.5">
          <Label>Provider</Label>
          <Select value={props.provider} onValueChange={(v) => props.onProvider(v as EmbeddingProvider)}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="openai">OpenAI</SelectItem>
              <SelectItem value="ollama">Ollama (local)</SelectItem>
              <SelectItem value="voyage">Voyage AI</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {props.provider === "ollama" ? (
          <p className="text-xs text-muted-foreground">Uses the same Ollama base URL as the chat provider ({props.ollamaBaseURL || "http://localhost:11434"}).</p>
        ) : (
          <>
            <div className="space-y-1.5">
              <Label>Base URL</Label>
              <Input
                placeholder={props.provider === "voyage" ? "https://api.voyageai.com/v1" : "https://api.openai.com/v1"}
                value={props.baseURL}
                onChange={(e) => props.onBaseURL(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label>API key {props.keySet && <span className="text-xs text-muted-foreground">(set — leave blank to keep)</span>}</Label>
              <Input type="password" placeholder={props.keySet ? "••••••••" : "your API key"} value={props.keyInput} onChange={(e) => props.onKeyInput(e.target.value)} />
            </div>
          </>
        )}

        <div className="space-y-1.5">
          <Label>Model</Label>
          <Input placeholder={props.provider === "voyage" ? "voyage-3" : props.provider === "ollama" ? "nomic-embed-text" : "text-embedding-3-small"} value={props.model} onChange={(e) => props.onModel(e.target.value)} />
        </div>
      </CardContent>
    </Card>
  )
}

// -- Diagnostics section (Sections 1-7, 9) ----------------------------------

function DiagnosticsSection({
  requestProvider,
  baseURL,
  model,
  embeddingModel,
}: {
  requestProvider: "ollama" | "anthropic" | "openai" | "embedding"
  baseURL?: string
  model?: string
  embeddingModel?: string
}) {
  const [report, setReport] = useState<ProviderDiagnosticsReport | null>(null)
  const [loading, setLoading] = useState(false)
  const [history, setHistory] = useState<DiagnosticsHistoryEntry[]>([])
  const [performance, setPerformance] = useState<PerformanceSummary | null>(null)
  const [pullProgress, setPullProgress] = useState<Record<string, OllamaPullProgress>>({})
  const pullAbort = useRef<AbortController | null>(null)

  const refreshHistoryAndPerf = useCallback(() => {
    getDiagnosticsHistory(requestProvider).then((r) => setHistory(r.items)).catch(() => {})
    getPerformanceSummary(requestProvider).then(setPerformance).catch(() => {})
  }, [requestProvider])

  useEffect(() => {
    refreshHistoryAndPerf()
  }, [refreshHistoryAndPerf])

  async function doRun(deep: boolean) {
    setLoading(true)
    try {
      const r = await runDiagnostics({ provider: requestProvider, base_url: baseURL, model, embedding_model: embeddingModel, deep })
      setReport(r)
      refreshHistoryAndPerf()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Diagnostics failed to run")
    } finally {
      setLoading(false)
    }
  }

  function handlePullModel(modelName: string) {
    if (!modelName) return
    pullAbort.current?.abort()
    const controller = new AbortController()
    pullAbort.current = controller
    setPullProgress((prev) => ({ ...prev, [modelName]: { status: "starting…" } }))
    streamOllamaPull(baseURL, modelName, (p) => {
      setPullProgress((prev) => ({ ...prev, [modelName]: p }))
      if (p.status === "success" || p.done) {
        toast.success(`Pulled ${modelName}`)
        doRun(false)
      }
      if (p.error) toast.error(p.error)
    }, controller.signal)
  }

  const activePull = Object.entries(pullProgress).find(([, p]) => !p.done && p.status !== "success" && !p.error)

  return (
    <div className="space-y-4 pt-4">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={() => doRun(false)} disabled={loading}>
          {loading ? <Loader2 className="animate-spin" /> : <Play />} Run diagnostics
        </Button>
        <Button size="sm" variant="outline" onClick={() => doRun(true)} disabled={loading}>
          <ShieldCheck /> Run full check (real generate/chat call)
        </Button>
        {report && report.status === "ok" && (
          <span className="flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 className="size-3.5" /> Verified {new Date(report.checked_at).toLocaleTimeString()}
          </span>
        )}
      </div>

      {activePull && (
        <div className="rounded-md border p-3 text-sm">
          <p className="mb-1 font-medium">Pulling {activePull[0]}…</p>
          <p className="text-xs text-muted-foreground">{activePull[1].status}</p>
          {!!activePull[1].total && (
            <Progress value={((activePull[1].completed ?? 0) / activePull[1].total) * 100} className="mt-2" />
          )}
        </div>
      )}

      <Card>
        <CardContent className="pt-6">
          <ProviderDiagnosticsPanel report={report} loading={loading} onPullModel={handlePullModel} onRefresh={() => doRun(false)} onRetest={() => doRun(false)} />
        </CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Performance</CardTitle>
          </CardHeader>
          <CardContent>
            <PerformancePanel summary={performance} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">Connection history</CardTitle>
          </CardHeader>
          <CardContent>
            <ConnectionHistoryPanel entries={history} />
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function BackendHealthSection() {
  const [report, setReport] = useState<BackendHealthReport | null>(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    getBackendHealth()
      .then(setReport)
      .catch((err) => toast.error(err instanceof ApiError ? err.message : "Failed to load backend health"))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  return (
    <Card className="mt-4">
      <CardContent className="pt-6">
        <BackendHealthPanel report={report} loading={loading} onRefresh={load} />
      </CardContent>
    </Card>
  )
}
