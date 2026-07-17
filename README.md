# onebox

**The All-in-One AI Backend — "PocketBase for AI Apps"**

One small binary: database, auth, files, vector search, RAG, and LLM gateway.

## What it is

Download one file, run it, and you have a complete backend for an AI application:
data, users, file uploads, vector search, retrieval-augmented generation (RAG),
and a managed gateway to LLM providers.

Today, building an AI product means wiring together a database, a separate
vector store, an embeddings pipeline, auth, file storage, and LLM key
management. onebox collapses all of that into a single binary with a clean
admin dashboard.

## Quickstart (target experience)

```
./onebox serve
```

Then open `http://localhost:8090/_/` for the admin dashboard, upload a PDF,
and ask a question about it — in under two minutes from download.

## Status

Early development. See [ROADMAP.md](ROADMAP.md) for the build plan.

## Scope (v0.1)

- **Core server** — single Go binary, HTTP server, config, migrations, admin dashboard
- **Data** — collections (tables) with typed fields, CRUD REST API, realtime subscriptions
- **Auth** — email/password, JWT sessions, OAuth (Google + GitHub), per-collection access rules
- **Files** — upload, store, serve files (local disk first, S3-compatible later)
- **RAG engine** — ingest PDF/TXT/MD, chunk, embed, store vectors, semantic search
- **LLM gateway** — provider-agnostic `/chat` endpoint (Anthropic, OpenAI, Ollama), caching, per-user rate/spend limits

## Explicitly out of scope for v0.1

No custom storage engine, no clustering/replication, no Postgres/MySQL backend,
no custom model training, no GraphQL/gRPC, no plugin marketplace, no SSO/SAML.
SQLite + single node + REST/SSE only. See [ROADMAP.md](ROADMAP.md) for the full
anti-scope list and rationale.

## License

TBD (MIT or Apache-2.0) — to be finalized before public launch.
