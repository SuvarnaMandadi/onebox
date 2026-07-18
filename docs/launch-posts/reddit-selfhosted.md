<!--
Draft only — not posted. r/selfhosted (and r/LocalLLaMA, similarly) cares
about: is it actually self-hostable with no phone-home, what does it
depend on, resource usage, and license. Lead with that, not marketing
language. Fill in [repo URL] once public.
-->

## Title

**onebox — a self-hosted, all-in-one AI backend (SQLite + auth + RAG + LLM gateway) in one Go binary**

## Body

I've been building onebox, a self-hosted backend for AI apps — think
"PocketBase, but for AI apps": one binary, SQLite storage, no external
services required to run it.

**What it does:**
- Collections (schema-defined tables) → REST CRUD + realtime SSE, like
  PocketBase
- Email/password auth with per-collection access rules
- File uploads (stored on local disk)
- A RAG engine: upload PDF/TXT/MD, it's chunked + embedded in the
  background, then you can ask questions grounded in your own documents
- An LLM gateway that proxies to Anthropic, OpenAI, **or a local Ollama
  instance** — so you can run the LLM side fully locally too, if you'd
  rather not send anything to a hosted API
- A small embedded admin dashboard, no separate service to run

**Self-hosting specifics:**
- Single static binary, no cgo, cross-compiles to Windows/Mac/Linux
- SQLite (WAL mode) — no Postgres, no separate vector DB container
- Data lives in one folder next to the binary (or wherever
  `ONEBOX_DATA_DIR` points)
- MIT licensed
- No telemetry, no phone-home
- A `Dockerfile` is included if you'd rather run it in a container
  (distroless base image, ~15MB)

**Honest limitations for v0.1:** no OAuth yet (email/password only), no
S3-compatible storage (local disk only), and vector search is brute-force
cosine similarity in Go rather than a proper ANN index — fine at the
scale of a personal project or small team's documents, not built for
millions of vectors. Details + why in the repo's ROADMAP.md.

Repo: [repo URL]

Feedback (especially "this breaks if you..." reports) very welcome.
