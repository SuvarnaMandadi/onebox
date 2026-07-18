# Example: resume autofill Chrome extension

A demo app built on onebox — not a separate product. A Manifest V3 Chrome
extension that reads a resume ingested into onebox's RAG engine, and fills
any web form's labeled fields from it.

**How it uses onebox:** the popup uploads a resume via `POST /api/rag/sources`
(the same ingestion pipeline the admin dashboard's RAG sources page uses).
The autofill button on a page calls `POST /api/rag/query` to retrieve the
relevant resume text, then `POST /api/llm/chat` with that text as grounding
context, asking for a strict JSON object mapping form field labels to
values — the "grounded in the resume" pattern from the API, just with
structured JSON output instead of a prose answer, since a form needs values
per field rather than a paragraph.

## 1. Start onebox with a provider configured

You need both an embedding provider (for ingestion) and an LLM (for
autofill) configured — env vars, or live in the admin dashboard's
Settings:

```bash
ONEBOX_EMBEDDING_API_KEY=sk-... ONEBOX_ANTHROPIC_API_KEY=sk-ant-... ./onebox
```

(Or point `ONEBOX_EMBEDDING_PROVIDER=ollama` / an Ollama model name at a
local Ollama instance — see the root [README](../../README.md).)

## 2. Load the extension

1. Open Chrome and go to `chrome://extensions`
2. Turn on **Developer mode** (top-right toggle)
3. Click **Load unpacked**
4. Select this folder (`examples/autofill-extension`)
5. Pin the extension (puzzle-piece icon in the toolbar → pin "onebox
   Resume Autofill") so it's easy to reach

## 3. Connect and upload a resume

1. Click the extension icon to open the popup
2. Set the **onebox server URL** (default `http://localhost:8090`)
3. **Sign up** (first time) or **log in** with an email/password — this
   creates/uses a regular onebox `_users` account, the same one that owns
   the uploaded resume
4. Set **Model** to a model your server can actually route to (default
   `claude-sonnet-5`; use an Ollama model name if that's what you
   configured in step 1 — see the root README's note on model-name
   routing)
5. Choose a resume file (`.pdf`, `.txt`, `.md`, or `.docx`) and click
   **Upload resume** — status updates from `Pending` → `Processing` →
   `Ready` as ingestion finishes in the background

## 4. Try it

1. Open [`test-form.html`](test-form.html) in Chrome (drag it into a tab,
   or `open examples/autofill-extension/test-form.html`)
2. A **"✨ Autofill with onebox"** button appears in the bottom-right
   corner (the content script only injects it on pages that have
   fillable form fields)
3. Click it — the extension reads every labeled field (Name, Email,
   Phone, Skills, Education, Work Experience), asks onebox to extract
   matching values from your uploaded resume, and fills them in

This works on real-world forms too, not just the test page — the content
script detects any `<label>`-associated input or textarea on any site.

## How it works (code map)

- [`manifest.json`](manifest.json) — Manifest V3 config
- [`background.js`](background.js) — service worker; holds the server
  URL + session token in `chrome.storage.local`, makes every onebox API
  call (login/signup, resume upload + status polling, the
  query-then-chat autofill extraction)
- [`content.js`](content.js) — injected into every page; detects
  fillable fields by their `<label>`, renders the floating button in a
  shadow root (so it can't collide with the host page's CSS), and fills
  matched fields
- [`popup.html`](popup.html) / [`popup.js`](popup.js) — the connect/
  upload UI, talks to `background.js` only via `chrome.runtime.sendMessage`
- [`test-form.html`](test-form.html) — the standalone sample form

## Limitations (it's a demo)

Single resume at a time (uploading a new one doesn't delete the old one
server-side — see the dashboard's RAG sources page to manage them). No
retry/backoff on the extraction call. Field matching is by label text
sent to the LLM, not a fixed schema, so unusual field labels may not
extract cleanly — that's expected for a demo of the API, not a resume-
parsing product.
