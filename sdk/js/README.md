# onebox-js

JavaScript/TypeScript SDK for [onebox](../../README.md) — a typed client
covering every REST endpoint (auth, collections, files, RAG, the LLM
gateway, usage, and realtime).

No runtime dependencies — built on native `fetch`, `FormData`, and
`EventSource`, so it works in any modern browser and Node 18+.

## Install

This SDK isn't published to npm yet (v0.1 — see the repo's ROADMAP.md).
For now, build it from source and reference it locally:

```bash
cd sdk/js
npm install
npm run build
```

## Quickstart

```ts
import { OneboxClient } from "onebox-js";

const client = new OneboxClient({ baseUrl: "http://localhost:8090" });

const { token } = await client.auth.signup("me@example.com", "hunter22222");
client.setToken(token);

interface Post {
  id: string;
  owner_id?: string;
  created: string;
  updated: string;
  title: string;
  published: boolean;
}

const posts = client.records<Post>("posts");
const created = await posts.create({ title: "hello world", published: true });
const { items } = await posts.list({ sort: "-created", limit: 10 });
```

## RAG

```ts
const source = await client.rag.uploadSource(fileBlob, "handbook.pdf");
// ingestion (extract/chunk/embed) runs in the background — poll until done:
let status = await client.rag.getSource(source.id);
while (status.status === "pending" || status.status === "processing") {
  await new Promise((r) => setTimeout(r, 1000));
  status = await client.rag.getSource(source.id);
}

const { answer, sources } = await client.rag.answer("what's our refund policy?");
```

## LLM gateway

```ts
// non-streaming
const { content } = await client.llm.chat("claude-sonnet-5", [{ role: "user", content: "hi" }]);

// streaming
await client.llm.chatStream("claude-sonnet-5", [{ role: "user", content: "hi" }], (delta) => {
  process.stdout.write(delta);
});
```

## Realtime

```ts
const es = client.subscribeRealtime((evt) => {
  console.log(evt.action, evt.collection, evt.record);
});
// es.close() when done
```

## Errors

Every non-2xx response throws `OneboxError` with `status`, `code`, and
`message` matching the server's `{code, message, details}` envelope:

```ts
try {
  await client.records("posts").get("missing-id");
} catch (e) {
  if (e instanceof OneboxError && e.code === "not_found") {
    // ...
  }
}
```

## Development

```bash
npm run build   # compile TypeScript -> dist/
npm test        # build, then run tests against the compiled output
```
