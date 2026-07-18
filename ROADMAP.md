# Roadmap

Working plan for onebox v0.1, the all-in-one AI backend. See [README.md](README.md)
for the pitch and scope.

## Anti-scope (v0.1) — do not do these

- No custom storage engine — SQLite is the engine.
- No horizontal scaling, clustering, or multi-node replication. Single node is
  a feature. Add litestream backups instead; revisit replication in v1.x.
- No Postgres/MySQL as alternative backends.
- No custom embedding/LLM model training — call providers and Ollama.
- No GraphQL, gRPC, plugins, workflow builders, cron dashboards, email
  template designers, or marketplace. REST + SSE only.
- No SDK sprawl — JS/TS first, Python second, nothing else until users ask.
- No enterprise features (SSO/SAML, audit logs, RBAC hierarchies).
- No paid cloud before the open-source core has real users.
- Cross-compilation for Windows/Mac/Linux is mandatory from day one.
- Provider API keys are encrypted at rest from day one — never plaintext.

## Month 1 — skeleton

- **Week 1**: Go module setup; HTTP server (chi router); config loading;
  SQLite connection in WAL mode; migration runner; health endpoint.
  Cross-compile check for all three OSes.
- **Week 2**: `_users` and `_admins` tables; signup/login/JWT middleware;
  password hashing (argon2id).
- **Week 3**: `_collections` registry; dynamic table creation from a JSON
  schema; CRUD endpoints with filtering and pagination.
- **Week 4**: Access-rules expression engine (owner checks, public/private
  to start); integration tests for every endpoint.

## Month 2 — usable backend

- **Week 5**: File upload/serve/delete; size and MIME limits.
- **Week 6**: Realtime via server-sent events on record changes.
- **Week 7–8**: Admin dashboard v1: login, collection editor, record browser,
  file browser, settings page.

## Month 3 — the AI core (differentiator)

> **Decision**: skipped sqlite-vec. It's a C extension, and
> `modernc.org/sqlite` (our pure-Go driver, chosen in Week 1 for
> cross-compiling to Win/Mac/Linux from one machine with no cgo) is a
> from-scratch Go reimplementation, not a wrapper around real SQLite — it
> can't load C extensions at all. Switching to `mattn/go-sqlite3` (cgo)
> would restore sqlite-vec but break the one-command cross-compile story
> from Week 1, requiring a C cross-toolchain (e.g. zig cc) per target OS.
> Instead: embeddings are stored as BLOBs in a plain `_rag_chunks` table
> and cosine similarity is computed in Go at query time (brute-force scan).
> Fine at the scale onebox targets (a handful of documents per user); a
> real ANN index can replace this later without changing the API, the same
> way replication was deferred to v1.x.

- **Week 9**: Embedding adapter interface; OpenAI-compatible + Ollama
  embedding providers. (No sqlite-vec — see decision above.)
- **Week 10**: Ingestion pipeline: PDF/TXT/MD extraction, chunking (~500
  tokens with overlap), async worker, status tracking.
- **Week 11**: `/api/rag/query` with cosine similarity top-k; relevance
  sanity tests on 3 real documents.
- **Week 12**: `/api/rag/answer`: retrieve, build prompt, call LLM, return
  answer plus source citations.

## Month 4 — the LLM gateway

- **Week 13**: Provider adapters (Anthropic, OpenAI, Ollama) behind one chat
  interface; streaming responses.
- **Week 14**: Response cache (hash of model+messages); per-user rate limits
  and monthly spend caps; `_usage` logging.
- **Week 15–16**: Dashboard v2: usage charts, spend by user, RAG source
  manager, provider key settings (encrypted).

## Month 5 — developer experience

- JS/TS SDK covering every endpoint, with typed collection helpers.
- Documentation site: 5-minute quickstart, one full tutorial ("build a
  chat-with-your-docs app"), API reference.
- Three example apps: docs Q&A, AI notes app, support-bot starter.
- Polish the killer demo: download to answering questions about an uploaded
  PDF in under two minutes.

## Month 6 — launch

- Public GitHub release (MIT license), binaries for Mac/Linux/Windows,
  Docker image.
- Launch posts: Hacker News (Show HN), Product Hunt, r/selfhosted,
  r/LocalLLaMA, dev.to. A 90-second demo video is the highest-leverage asset.
- Then: answer every issue fast for 6–12 months.

## Reference: system tables

| Table | Purpose |
|---|---|
| `_users` | End-user accounts |
| `_admins` | Dashboard administrators |
| `_collections` | Registry of user-defined collections, schema, access rules |
| `_files` | File metadata |
| `_rag_sources` | Ingested documents |
| `_rag_chunks` | Text chunks + embeddings (sqlite-vec) |
| `_usage` | Every LLM/embedding call: tokens, cost, cache status |
| `_settings` | Provider API keys (encrypted), limits, config |

## Reference: API surface (v0.1)

- `POST /api/auth/signup`, `POST /api/auth/login`, `GET /api/auth/oauth/:provider`
- `GET|POST /api/collections/:name/records`, `GET|PATCH|DELETE /api/collections/:name/records/:id`
- `GET /api/realtime` (SSE)
- `POST /api/files`, `GET|DELETE /api/files/:id`
- `POST /api/rag/sources`, `GET|DELETE /api/rag/sources/:id`
- `POST /api/rag/query`, `POST /api/rag/answer`
- `POST /api/llm/chat`
- `GET /api/usage`

Consistent JSON error envelope: `{code, message, details}`. Cursor pagination.
PocketBase-style filter syntax on list endpoints.
