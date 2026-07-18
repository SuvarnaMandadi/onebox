import type {
  AuthResponse,
  AuthRecord,
  Collection,
  CollectionSchema,
  CollectionRules,
  RecordBase,
  ListParams,
  ListResponse,
  FileRecord,
  FileListResponse,
  RAGSource,
  RAGSourceListResponse,
  RAGScoredChunk,
  RAGAnswer,
  ChatMessage,
  ChatResponse,
  UsageRecord,
  RealtimeEvent,
  UpdateProfileParams,
  CreatePasswordResetResponse,
} from "./types.js";

/** Thrown for any non-2xx response, using onebox's {code,message,details} envelope. */
export class OneboxError extends Error {
  code: string;
  status: number;
  details?: unknown;

  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message);
    this.name = "OneboxError";
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

export interface OneboxClientOptions {
  /** e.g. "http://localhost:8090" — no trailing slash needed. */
  baseUrl: string;
  /** A _users or _admins session token, if you already have one. */
  token?: string;
}

/** Typed client for every onebox REST endpoint. */
export class OneboxClient {
  private baseUrl: string;
  private token: string;

  constructor(options: OneboxClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.token = options.token ?? "";
  }

  /** Store the session token from auth.signup/login (or admins.signup/login). */
  setToken(token: string): void {
    this.token = token;
  }

  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    if (this.token) headers.set("Authorization", `Bearer ${this.token}`);
    if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }

    const res = await fetch(this.baseUrl + path, { ...init, headers });
    if (res.status === 204) return undefined as T;

    const isJSON = (res.headers.get("content-type") ?? "").includes("application/json");
    const body: unknown = isJSON ? await res.json() : await res.text();

    if (!res.ok) {
      const envelope = (isJSON && body && typeof body === "object" ? body : {}) as {
        code?: string;
        message?: string;
        details?: unknown;
      };
      throw new OneboxError(res.status, envelope.code ?? "unknown_error", envelope.message ?? String(body), envelope.details);
    }
    return body as T;
  }

  private async rawRequest(path: string): Promise<Blob> {
    const headers = new Headers();
    if (this.token) headers.set("Authorization", `Bearer ${this.token}`);
    const res = await fetch(this.baseUrl + path, { headers });
    if (!res.ok) {
      const body = (await res.json().catch(() => ({}))) as { code?: string; message?: string };
      throw new OneboxError(res.status, body.code ?? "unknown_error", body.message ?? res.statusText);
    }
    return res.blob();
  }

  auth = {
    signup: (email: string, password: string, opts: { firstName?: string; lastName?: string } = {}) =>
      this.request<AuthResponse>("/api/auth/signup", {
        method: "POST",
        body: JSON.stringify({ email, password, first_name: opts.firstName, last_name: opts.lastName }),
      }),
    login: (email: string, password: string) =>
      this.request<AuthResponse>("/api/auth/login", { method: "POST", body: JSON.stringify({ email, password }) }),

    /** The signed-in _users account's own profile. */
    me: () => this.request<AuthRecord>("/api/auth/me"),

    /** Updates the caller's own profile. A changed email is re-checked for uniqueness. */
    updateProfile: (params: UpdateProfileParams) =>
      this.request<AuthRecord>("/api/auth/me", { method: "PATCH", body: JSON.stringify(params) }),

    /** Uploads an avatar image through the same file storage as any other upload. */
    uploadAvatar: (file: Blob, filename?: string) => {
      const form = new FormData();
      form.append("file", file, filename);
      return this.request<AuthRecord>("/api/auth/me/avatar", { method: "POST", body: form });
    },

    changePassword: (currentPassword: string, newPassword: string) =>
      this.request<{ ok: boolean }>("/api/auth/change-password", {
        method: "POST",
        body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
      }),

    /**
     * Consumes a one-time reset token minted by an admin (see
     * admins.createPasswordReset) — there's no SMTP-based self-service
     * "forgot password" flow yet, so this is always called with a token a
     * user received out of band.
     */
    resetPassword: (token: string, newPassword: string) =>
      this.request<{ ok: boolean }>("/api/auth/reset-password", {
        method: "POST",
        body: JSON.stringify({ token, new_password: newPassword }),
      }),
  };

  admins = {
    signup: (email: string, password: string) =>
      this.request<AuthResponse>("/api/admins/signup", { method: "POST", body: JSON.stringify({ email, password }) }),
    login: (email: string, password: string) =>
      this.request<AuthResponse>("/api/admins/login", { method: "POST", body: JSON.stringify({ email, password }) }),

    /**
     * Admin-only: mints a one-time password reset token for a _users
     * account by email. Since v0.2 has no SMTP integration, the admin
     * hands this token to the user out of band (chat, a ticket, in
     * person); the user then calls auth.resetPassword(token, ...).
     */
    createPasswordReset: (email: string) =>
      this.request<CreatePasswordResetResponse>("/api/admins/password-resets", {
        method: "POST",
        body: JSON.stringify({ email }),
      }),
  };

  collections = {
    create: (name: string, schema: CollectionSchema, rules?: CollectionRules) =>
      this.request<Collection>("/api/collections", { method: "POST", body: JSON.stringify({ name, schema, rules }) }),
    list: () => this.request<{ items: Collection[] }>("/api/collections").then((r) => r.items),
    get: (name: string) => this.request<Collection>(`/api/collections/${encodeURIComponent(name)}`),
    delete: (name: string) => this.request<void>(`/api/collections/${encodeURIComponent(name)}`, { method: "DELETE" }),
  };

  /**
   * Typed CRUD for one collection's records, e.g.:
   *   interface Post extends RecordBase { title: string }
   *   const posts = client.records<Post>("posts");
   *   const { items } = await posts.list();
   */
  records<T extends RecordBase>(collectionName: string) {
    const base = `/api/collections/${encodeURIComponent(collectionName)}/records`;
    return {
      list: (params: ListParams = {}) => {
        const qs = new URLSearchParams();
        if (params.filter) qs.set("filter", params.filter);
        if (params.sort) qs.set("sort", params.sort);
        if (params.limit) qs.set("limit", String(params.limit));
        if (params.cursor) qs.set("cursor", params.cursor);
        const query = qs.toString();
        return this.request<ListResponse<T>>(base + (query ? `?${query}` : ""));
      },
      create: (data: Partial<T>) => this.request<T>(base, { method: "POST", body: JSON.stringify(data) }),
      get: (id: string) => this.request<T>(`${base}/${id}`),
      update: (id: string, data: Partial<T>) => this.request<T>(`${base}/${id}`, { method: "PATCH", body: JSON.stringify(data) }),
      delete: (id: string) => this.request<void>(`${base}/${id}`, { method: "DELETE" }),
    };
  }

  files = {
    upload: (file: Blob, filename?: string) => {
      const form = new FormData();
      form.append("file", file, filename);
      return this.request<FileRecord>("/api/files", { method: "POST", body: form });
    },
    list: (params: { limit?: number; cursor?: string } = {}) => {
      const qs = new URLSearchParams();
      if (params.limit) qs.set("limit", String(params.limit));
      if (params.cursor) qs.set("cursor", params.cursor);
      const query = qs.toString();
      return this.request<FileListResponse>("/api/files" + (query ? `?${query}` : ""));
    },
    download: (id: string) => this.rawRequest(`/api/files/${id}`),
    delete: (id: string) => this.request<void>(`/api/files/${id}`, { method: "DELETE" }),
  };

  rag = {
    uploadSource: (file: Blob, filename?: string) => {
      const form = new FormData();
      form.append("file", file, filename);
      return this.request<RAGSource>("/api/rag/sources", { method: "POST", body: form });
    },
    listSources: (params: { limit?: number; cursor?: string } = {}) => {
      const qs = new URLSearchParams();
      if (params.limit) qs.set("limit", String(params.limit));
      if (params.cursor) qs.set("cursor", params.cursor);
      const query = qs.toString();
      return this.request<RAGSourceListResponse>("/api/rag/sources" + (query ? `?${query}` : ""));
    },
    getSource: (id: string) => this.request<RAGSource>(`/api/rag/sources/${id}`),
    deleteSource: (id: string) => this.request<void>(`/api/rag/sources/${id}`, { method: "DELETE" }),
    query: (query: string, topK = 5) =>
      this.request<{ results: RAGScoredChunk[] }>("/api/rag/query", {
        method: "POST",
        body: JSON.stringify({ query, top_k: topK }),
      }),
    answer: (query: string, topK = 5) =>
      this.request<RAGAnswer>("/api/rag/answer", { method: "POST", body: JSON.stringify({ query, top_k: topK }) }),
  };

  llm = {
    chat: (model: string, messages: ChatMessage[]) =>
      this.request<ChatResponse>("/api/llm/chat", { method: "POST", body: JSON.stringify({ model, messages }) }),

    /**
     * Streams a chat completion, calling onDelta as text arrives, and
     * resolves with the full assembled content. Uses a raw fetch + SSE
     * parse rather than EventSource, since EventSource only supports GET
     * and this is a POST.
     */
    chatStream: async (model: string, messages: ChatMessage[], onDelta: (delta: string) => void): Promise<string> => {
      const headers = new Headers({ "Content-Type": "application/json" });
      if (this.token) headers.set("Authorization", `Bearer ${this.token}`);

      const res = await fetch(this.baseUrl + "/api/llm/chat", {
        method: "POST",
        headers,
        body: JSON.stringify({ model, messages, stream: true }),
      });
      if (!res.ok || !res.body) {
        const body = (await res.json().catch(() => ({}))) as { code?: string; message?: string };
        throw new OneboxError(res.status, body.code ?? "unknown_error", body.message ?? res.statusText);
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let full = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const parts = buffer.split("\n\n");
        buffer = parts.pop() ?? "";
        for (const line of parts) {
          if (!line.startsWith("data: ")) continue;
          const payload = line.slice("data: ".length);
          if (payload === "[DONE]") return full;
          const msg = JSON.parse(payload) as { delta?: string; error?: string };
          if (msg.error) throw new OneboxError(500, "stream_error", msg.error);
          if (msg.delta) {
            full += msg.delta;
            onDelta(msg.delta);
          }
        }
      }
      return full;
    },
  };

  usage = {
    list: (params: { userId?: string; from?: string; to?: string } = {}) => {
      const qs = new URLSearchParams();
      if (params.userId) qs.set("user_id", params.userId);
      if (params.from) qs.set("from", params.from);
      if (params.to) qs.set("to", params.to);
      const query = qs.toString();
      return this.request<{ items: UsageRecord[]; total_cost_estimate: number }>(
        "/api/usage" + (query ? `?${query}` : "")
      );
    },
  };

  /**
   * Subscribes to record-change events. Requires a browser (or a Node
   * EventSource polyfill) — this thinly wraps EventSource, passing the
   * token as a query param since EventSource can't set headers.
   */
  subscribeRealtime(onEvent: (evt: RealtimeEvent) => void): EventSource {
    const es = new EventSource(this.baseUrl + "/api/realtime?token=" + encodeURIComponent(this.token));
    es.addEventListener("record_change", (e) => {
      onEvent(JSON.parse((e as MessageEvent).data));
    });
    return es;
  }
}
