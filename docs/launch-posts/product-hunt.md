<!--
Draft only — not posted. Product Hunt needs: a tagline (<=60 chars), a
description, a gallery (screenshots/GIF of the admin dashboard + the
docs-qa example running), and a "maker comment" posted at launch. Fill
in links once the repo is public and (if applicable) a hosted demo exists.
-->

## Tagline (60 char max)

**The all-in-one AI backend — auth, RAG, and LLM gateway, one binary**

## Description

onebox is "PocketBase for AI apps": one small Go binary that gives you a
database with realtime CRUD, auth, file storage, a RAG engine (upload a
doc, get grounded answers), and a provider-agnostic LLM gateway
(Anthropic/OpenAI/Ollama) with caching and per-user spend limits — all
the boilerplate a solo dev or small team rebuilds for every AI prototype,
already wired together.

Free and self-hostable (MIT license). Download one file, run it, and
you have a backend — no Postgres, no separate vector DB, no docker-compose.

**Built for:** solo developers and small teams building AI products who
are tired of assembling the same five services before they can start on
the actual product.

**What's in v0.1:**
- Collections with typed fields → instant REST CRUD + realtime SSE
- Email/password auth, per-collection access rules
- File uploads
- RAG: PDF/TXT/MD/DOCX ingestion → chunk → embed → cosine-similarity search
  → grounded LLM answers with citations
- LLM gateway: one endpoint, three providers, response caching, rate
  limits, spend caps, usage logging
- An embedded admin dashboard
- A JS/TS SDK and three starter apps (docs Q&A, AI notes, support bot)

## Maker comment (post at launch)

Hey Product Hunt! I built onebox after noticing I'd spend the first
afternoon of every AI-app prototype wiring together a database, auth,
file storage, a vector store, and LLM key management — before writing
any of the actual product.

The single-binary-everywhere promise mattered enough that I made a real
trade-off for it: the roadmap called for `sqlite-vec` for vector search,
but it's a C extension, and I wanted `go build` to cross-compile to
Windows/Mac/Linux with no cgo toolchain. Those conflict, so v0.1 does
brute-force cosine similarity in Go instead — slower at large scale, but
correct, simple, and keeps the binary portable.

Would love your feedback — especially if you've hit the "too much
plumbing before the interesting part" problem building AI prototypes
yourself.
