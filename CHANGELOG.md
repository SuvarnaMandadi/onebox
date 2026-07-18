# Changelog

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
