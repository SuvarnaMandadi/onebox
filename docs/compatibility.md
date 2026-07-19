# Using onebox from any frontend

onebox is a plain REST API over HTTP — there's no SDK requirement, no
required runtime, and no CORS restriction by default (see
`ONEBOX_CORS_ORIGINS` in the README to lock that down for production).
If your language can make an HTTP request and parse JSON, it can use
onebox. The `sdk/js` package is a convenience for JS/TS projects, not a
requirement.

Every example below signs in, then lists records from a `notes`
collection.

## JavaScript (no SDK, just `fetch`)

```js
const res = await fetch("http://localhost:8090/api/auth/login", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ email: "you@example.com", password: "hunter22222" }),
});
const { token } = await res.json();

const notes = await fetch("http://localhost:8090/api/collections/notes/records", {
  headers: { Authorization: `Bearer ${token}` },
}).then((r) => r.json());
```

## Python

```python
import requests

login = requests.post("http://localhost:8090/api/auth/login", json={
    "email": "you@example.com",
    "password": "hunter22222",
}).json()
token = login["token"]

notes = requests.get(
    "http://localhost:8090/api/collections/notes/records",
    headers={"Authorization": f"Bearer {token}"},
).json()
```

## curl

```bash
TOKEN=$(curl -s -X POST http://localhost:8090/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"you@example.com","password":"hunter22222"}' | jq -r .token)

curl http://localhost:8090/api/collections/notes/records \
  -H "Authorization: Bearer $TOKEN"
```

The same pattern — POST credentials, get a bearer token, send it as
`Authorization: Bearer <token>` on every subsequent request — works
identically from Go, Ruby, PHP, Swift, or a mobile app. See
[api-reference.md](api-reference.md) for the full endpoint list.

## Migrating existing data in

If you're moving to onebox from another backend, export your existing
data to JSON or CSV and use each collection's **Import** button on the
Backups page in the admin dashboard: it previews the file's columns,
lets you map each one to a schema field (or skip it), and reports how
many rows imported successfully. There's no need to hand-write insert
scripts for a one-time migration — though the plain REST API above works
fine for that too, if you'd rather script it (e.g. a loop of `POST
/api/collections/:name/records` calls from whatever language your export
data is already in).
