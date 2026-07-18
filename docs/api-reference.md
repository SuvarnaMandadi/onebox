# API reference

Base URL: wherever you run onebox, e.g. `http://localhost:8090`.

- All responses are JSON. Errors use `{code, message, details}`.
- Auth is a `Authorization: Bearer <token>` header, from a `_users` or
  `_admins` session token. Two exceptions: file downloads and the
  realtime stream also accept `?token=` as a query param, since
  `<img>`/`<a>` tags and `EventSource` can't set custom headers.
- List endpoints use keyset cursor pagination: `?limit=30&cursor=...`,
  response includes `nextCursor` (empty string when there's no next page).

## Auth

| Endpoint | Method | Auth | Body | Notes |
|---|---|---|---|---|
| `/api/auth/signup` | POST | none | `{email, password}` | Creates a `_users` account. Returns `{token, record}`. |
| `/api/auth/login` | POST | none | `{email, password}` | Returns `{token, record}`. |
| `/api/admins/signup` | POST | none | `{email, password}` | Only works before any admin exists (bootstrap). |
| `/api/admins/login` | POST | none | `{email, password}` | |

## Collections (schema management, admin-only)

| Endpoint | Method | Auth | Body |
|---|---|---|---|
| `/api/collections` | POST | admin | `{name, schema: {fields: [{name, type, required}]}, rules?}` |
| `/api/collections` | GET | admin | — |
| `/api/collections/:name` | GET | admin | — |
| `/api/collections/:name` | DELETE | admin | — |

Field `type` is one of `text`, `number`, `bool`, `date`, `json`. `rules`
sets `list`/`view`/`create`/`update`/`delete` each to `public`,
`authenticated`, or `owner` (default: authenticated, except
update/delete which default to owner). Admins always bypass rules.

## Records (the data API)

| Endpoint | Method | Auth (per collection rules) |
|---|---|---|
| `/api/collections/:name/records` | GET | per `list` rule |
| `/api/collections/:name/records` | POST | per `create` rule |
| `/api/collections/:name/records/:id` | GET | per `view` rule |
| `/api/collections/:name/records/:id` | PATCH | per `update` rule |
| `/api/collections/:name/records/:id` | DELETE | per `delete` rule |

List query params: `filter=field=value,field2=value2` (equality, ANDed),
`sort=created` or `sort=-created` (default), `limit`, `cursor`.

## Files

| Endpoint | Method | Auth |
|---|---|---|
| `/api/files` | POST (multipart, field `file`) | any signed-in user or admin |
| `/api/files` | GET | admin only |
| `/api/files/:id` | GET | owner or admin |
| `/api/files/:id` | DELETE | owner or admin |

## RAG

| Endpoint | Method | Auth | Body |
|---|---|---|---|
| `/api/rag/sources` | POST (multipart, field `file`) | any signed-in user or admin | `.pdf`/`.txt`/`.md`/`.docx` only |
| `/api/rag/sources` | GET | admin only | — |
| `/api/rag/sources/:id` | GET | owner or admin | — |
| `/api/rag/sources/:id` | DELETE | owner or admin | — |
| `/api/rag/query` | POST | any signed-in user or admin | `{query, top_k?}` → `{results: [{source_id, text, score}]}` |
| `/api/rag/answer` | POST | any signed-in user or admin | `{query, top_k?}` → `{answer, sources}` |

Ingestion (extract → chunk → embed) runs asynchronously; poll
`GET /api/rag/sources/:id` for `status`: `pending` → `processing` →
`done`/`error`. Query/answer only search the caller's own ingested
chunks, unless the caller is an admin (searches everyone's).

## LLM gateway

| Endpoint | Method | Auth | Body |
|---|---|---|---|
| `/api/llm/chat` | POST | any signed-in user or admin | `{model, messages: [{role, content}], stream?}` |

`model` picks the provider by name prefix: `claude*` → Anthropic,
`gpt*`/`o1*`/`o3*`/`text-*` → OpenAI, anything else → Ollama. Non-streaming
responses are cached for 1h by `sha256(model + messages)`. Set
`"stream": true` for an SSE response (`data: {"delta": "..."}` per chunk,
ending `data: [DONE]`) — streamed responses bypass the cache.

Every call (cached, streamed, or not, including RAG's internal answer
calls) is subject to per-user rate limits (`ONEBOX_RATE_LIMIT_PER_MINUTE`)
and a monthly spend cap (`ONEBOX_MONTHLY_SPEND_CAP_USD`), and is logged.

## Usage

| Endpoint | Method | Auth | Query params |
|---|---|---|---|
| `/api/usage` | GET | any signed-in user or admin | `user_id` (admin only), `from`, `to` (ISO8601) |

Regular users only ever see their own usage. Response:
`{items: [...], total_cost_estimate}`.

## Settings (admin-only, encrypted at rest)

| Endpoint | Method | Body |
|---|---|---|
| `/api/settings` | GET | — (secrets report `{set: bool}`, never plaintext) |
| `/api/settings` | PUT | partial `{key: value}` — see keys below |

Known keys: `anthropic_api_key`, `anthropic_model`, `openai_api_key`,
`openai_base_url`, `embedding_provider`, `embedding_api_key`,
`embedding_base_url`, `embedding_model`, `ollama_base_url`. Saving
hot-reloads the live provider clients — no restart required.

## Realtime

`GET /api/realtime` (SSE, `Authorization` header or `?token=`) streams
`event: record_change` with `data: {action, collection, record}` for
every create/update/delete, filtered per-connection by that collection's
`view` rule (so a client never receives an event for a record it
couldn't otherwise fetch).

## Health

`GET /api/health` → `{status: "ok"}`, or 503 if the database is
unreachable. No auth required.
