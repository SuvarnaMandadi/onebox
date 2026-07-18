<!--
Draft only — not posted. Fill in the [repo URL] once the repo is public,
and swap in a real demo GIF/video link before posting. HN convention:
post from your own account, be present in the comments for the first
few hours, and don't reply defensively to skepticism — engage with the
actual technical questions.
-->

# Show HN: onebox – an all-in-one AI backend in one Go binary (SQLite, auth, RAG, LLM gateway)

Title (80 char limit): **Show HN: onebox – all-in-one AI backend, one binary (SQLite, RAG, LLM gateway)**

URL: [repo URL]

---

Hi HN,

A small demo of what this is for: I built a Chrome extension
([examples/autofill-extension](examples/autofill-extension)) that reads a
resume you upload once, and then fills any web form's labeled fields from
it — click "Autofill" on a job application and it fills name, email,
skills, education, work experience. It's just two calls to onebox's own
REST API (`/api/rag/query` to retrieve the relevant resume text, then
`/api/llm/chat` grounded in it, asking for structured JSON) — no separate
backend behind the extension, no server I stood up for the demo. That's
the pitch: this is what "one binary" gets you.

I built onebox because every time I wanted to prototype an AI app, I'd
spend the first afternoon wiring together the *same* five things: a
database, auth, file storage, a vector store, and LLM API key
management — before writing a single line of the actual product.

onebox is a single Go binary that does all of it:

- SQLite (WAL mode) with dynamic collections — define a schema, get a
  REST CRUD API and realtime subscriptions for free, PocketBase-style
- Email/password auth with per-collection access rules
  (public/authenticated/owner)
- File upload/storage
- A RAG engine: upload a PDF/TXT/MD/DOCX, it gets chunked and embedded in the
  background, then `/api/rag/answer` gives you a grounded answer with
  citations
- An LLM gateway: one `/api/llm/chat` endpoint routes to Anthropic,
  OpenAI, or a local Ollama model by model name, with response caching,
  per-user rate limits, and a monthly spend cap so you don't get a
  surprise bill
- An embedded admin dashboard (plain HTML/JS, no build step)

The interesting technical decision: the roadmap originally called for
`sqlite-vec` for vector search, but that's a C extension, and I wanted
`go build` to cross-compile to Windows/Mac/Linux from one machine with
no cgo. Those two goals conflict — `sqlite-vec` can't load into a
pure-Go SQLite driver. So vector search is brute-force cosine similarity
computed in Go over embeddings stored as BLOBs. It's slower than a real
ANN index at scale, but it's correct, it's simple, and the single-binary
cross-compile story stays intact. Happy to talk through that trade-off
in the comments.

It's MIT licensed, self-hostable for free. Would love feedback,
especially from anyone who's hit the same "too much boilerplate before
the interesting part" problem building AI prototypes.

[repo URL]
