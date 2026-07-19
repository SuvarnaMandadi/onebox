# Changelog

## v0.2.1

Fixes and additions from hand-testing v0.2.0's dashboard redesign, in the
order they were reported.

**Recovery phrases**
- A 12-word recovery phrase (onebox's own embedded word list, BIP39-style
  mechanism but not a claim of wordlist compatibility) is generated for
  every new account, admin or user, and shown exactly once right after
  signup in an "Emergency Kit" screen — account email, server URL, the
  phrase, and a "Download Emergency Kit (PDF)" button (the browser's
  native print-to-PDF, not a new dependency) — with a mandatory
  "I've saved this" acknowledgement before continuing. Only a hash of the
  phrase is ever stored, the same as a password.
- The Forgot Password page now verifies email + recovery phrase and lets
  you set a new password directly, no admin required. The admin-generated
  reset code from v0.2.0 is kept as a secondary fallback (toggle link).
- The Account page (both roles) can regenerate the phrase — it requires
  the current password and immediately invalidates the old phrase.

**Roles**
- The first account created on a fresh instance is now always the admin —
  the signup page checks the new `GET /api/setup-status` and drops the
  confusing admin/user toggle for that case entirely.
- Admins can promote a user to admin, or demote an admin, from a new
  Settings → Admins panel (`POST /api/admins/promote` /
  `POST /api/admins/demote` / `GET /api/admins`). Promotion creates a
  separate admin login via a one-time reset code, since `_admins` and
  `_users` are independent identity tables; demoting the last remaining
  admin is refused.
- `_admins` gained `first_name`, `last_name`, `phone`, `avatar_file_id`,
  mirroring `_users` — the Account page is now a real profile editor for
  admins too, not a read-only stub.
- Admin-only sidebar items (Collections, Settings) are no longer hidden
  from regular users — they show grayed out with a lock badge and explain
  themselves via a toast on click instead of silently failing.

**Fixes**
- Profile photos now actually show up: avatars are served from an
  authenticated endpoint, and a plain `<img src>` has no way to attach a
  bearer token, so it silently 404'd. Avatars are now fetched once as an
  authenticated blob and cached.
- Login/signup errors are specific: "No account found with this email"
  (with a Sign up link) vs "Invalid email or password" for a wrong
  password on an account that exists. Fixed the underlying bug — the
  dashboard treated *any* 401 as an expired session, including a plain
  wrong-password response from the login endpoint itself, and rewrote it
  to a confusing "session expired" message.
- The Home page no longer shows stale stats — clicking a nav link for the
  page you're already on didn't change the URL hash, so the router never
  re-rendered; it now forces a refresh in that case.

**Settings**
- Provider configuration is grouped into cards (Anthropic, OpenAI-
  compatible, Ollama, Embedding) with sensible defaults pre-filled, each
  with a real "Test connection" button (`POST /api/settings/test-connection`)
  that makes one minimal, non-billing request and reports success or
  failure immediately.
- RAG ingestion failures are now classified into plain-language causes
  (provider unreachable, wrong URL, bad API key, timeout) instead of a
  raw Go error string.

## v0.2.0

**Dashboard**
- New Home page (default landing after login): a greeting, a stat-card grid
  (collections/records for admins; documents, files, and AI usage for
  everyone), and a recent-activity feed. The sidebar logo always returns here.
- New Account page: profile (name, phone, avatar upload with initials
  fallback), editable email (re-checked for uniqueness, case-insensitive),
  and a change-password form. Admin sessions get a reduced read-only view,
  since `_admins` has no profile columns.
- Separate Login and Signup pages (replacing one combined login view), plus
  a Forgot-password page. Both support a regular user or an admin session
  via a secondary role-switch link. Inline validation, a show/hide password
  toggle, and loading states throughout.
- Admin-assisted password reset: since there's no SMTP integration yet, an
  admin generates a one-time reset token for a user from Settings and hands
  it to them directly; the user redeems it on the Forgot-password page.
- Visual pass: an accent gradient token, a hand-rolled SVG gradient
  background on the auth pages, card hover-lift and page/stat-card entrance
  animations (`prefers-reduced-motion` respected), and both light and dark
  themes tuned rather than auto-inverted. No new dependencies — everything
  is still embedded via `go:embed`.
- (from the previous redesign pass) Sidebar layout, toast notifications,
  themed confirm dialogs, loading-state buttons, dark mode, and a usage chart.

**API**
- `_users` gains `first_name`, `last_name`, `phone`, `avatar_file_id`.
  New endpoints: `GET/PATCH /api/auth/me`, `POST /api/auth/me/avatar`,
  `POST /api/auth/change-password`, `POST /api/auth/reset-password`, and
  the admin-only `POST /api/admins/password-resets`.
- `GET /api/files` and `GET /api/rag/sources` are no longer admin-only —
  a regular user now sees their own files/sources; admins still see
  everyone's. Both responses gained a `total` count, and RAG sources a
  `status_counts` breakdown.
- `GET /api/collections` items now include `record_count`.
- DOCX ingestion for RAG sources (`.pdf`, `.txt`, `.md`, `.docx`).
- Unsupported RAG file uploads now return a clear, structured error instead
  of a generic failure.
- Fixed: collection/field names rejected capital letters.
- Fixed: `/api/rag/answer` usage logging always recorded `"anthropic"` as
  the provider regardless of which provider the model actually routed to.

**SDK (`sdk/js`)**
- `auth.me`, `auth.updateProfile`, `auth.uploadAvatar`, `auth.changePassword`,
  `auth.resetPassword`, `admins.createPasswordReset`, `rag.listSources`.
- `AuthRecord`, `Collection`, file/RAG-source list response types updated
  to match the API additions above.

**Examples**
- New: [`examples/autofill-extension`](examples/autofill-extension) — a
  Manifest V3 Chrome extension that fills a web form from a resume
  ingested into onebox's RAG engine.

## v0.1.0

Initial public release: core server, dynamic collections, auth, files,
realtime, the RAG engine (PDF/TXT/MD, brute-force cosine similarity), the
LLM gateway, an admin dashboard, a JS/TS SDK, three example apps, and
release automation.
