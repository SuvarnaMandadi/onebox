import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { OneboxClient, OneboxError } from "../dist/index.js";

let originalFetch;
let calls;

before(() => {
  originalFetch = globalThis.fetch;
});

after(() => {
  globalThis.fetch = originalFetch;
});

function mockFetch(handler) {
  calls = [];
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), init });
    return handler(String(url), init);
  };
}

function jsonResponse(status, body) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

test("auth.signup posts to the right path with the right body", async () => {
  mockFetch(async () => jsonResponse(201, { token: "tok123", record: { id: "u1", email: "a@example.com" } }));

  const client = new OneboxClient({ baseUrl: "http://localhost:8090" });
  const resp = await client.auth.signup("a@example.com", "hunter22222");

  assert.equal(calls[0].url, "http://localhost:8090/api/auth/signup");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body), { email: "a@example.com", password: "hunter22222" });
  assert.equal(resp.token, "tok123");
  assert.equal(resp.record.email, "a@example.com");
});

test("setToken adds an Authorization header on subsequent requests", async () => {
  mockFetch(async () => jsonResponse(200, { items: [] }));

  const client = new OneboxClient({ baseUrl: "http://localhost:8090" });
  client.setToken("my-token");
  await client.collections.list();

  const headers = new Headers(calls[0].init.headers);
  assert.equal(headers.get("Authorization"), "Bearer my-token");
});

test("non-2xx responses throw OneboxError with the server's code/message", async () => {
  mockFetch(async () => jsonResponse(404, { code: "not_found", message: "record not found" }));

  const client = new OneboxClient({ baseUrl: "http://localhost:8090" });
  await assert.rejects(
    () => client.records("posts").get("missing-id"),
    (err) => {
      assert.ok(err instanceof OneboxError);
      assert.equal(err.status, 404);
      assert.equal(err.code, "not_found");
      assert.equal(err.message, "record not found");
      return true;
    }
  );
});

test("records().list() builds a query string from list params", async () => {
  mockFetch(async () => jsonResponse(200, { items: [], nextCursor: "" }));

  const client = new OneboxClient({ baseUrl: "http://localhost:8090" });
  await client.records("posts").list({ filter: "published=true", sort: "-created", limit: 10, cursor: "abc" });

  const url = new URL(calls[0].url);
  assert.equal(url.pathname, "/api/collections/posts/records");
  assert.equal(url.searchParams.get("filter"), "published=true");
  assert.equal(url.searchParams.get("sort"), "-created");
  assert.equal(url.searchParams.get("limit"), "10");
  assert.equal(url.searchParams.get("cursor"), "abc");
});

test("files.upload() sends multipart form data without a manual Content-Type", async () => {
  mockFetch(async () => jsonResponse(201, { id: "f1", filename: "hello.txt", size: 5, mime: "text/plain", created: "now" }));

  const client = new OneboxClient({ baseUrl: "http://localhost:8090" });
  const blob = new Blob(["hello"], { type: "text/plain" });
  await client.files.upload(blob, "hello.txt");

  assert.ok(calls[0].init.body instanceof FormData);
  const headers = new Headers(calls[0].init.headers);
  // Content-Type (with the multipart boundary) must be left for fetch to
  // set itself — setting it manually on a FormData body breaks the
  // boundary and the server can't parse the upload.
  assert.equal(headers.has("Content-Type"), false);
});

test("llm.chatStream() assembles deltas and reports each one via onDelta", async () => {
  const sseBody =
    'data: {"delta":"Hello"}\n\n' + 'data: {"delta":" world"}\n\n' + "data: [DONE]\n\n";

  mockFetch(async () => new Response(sseBody, { status: 200, headers: { "content-type": "text/event-stream" } }));

  const client = new OneboxClient({ baseUrl: "http://localhost:8090" });
  const deltas = [];
  const full = await client.llm.chatStream("claude-sonnet-5", [{ role: "user", content: "hi" }], (d) => deltas.push(d));

  assert.equal(full, "Hello world");
  assert.deepEqual(deltas, ["Hello", " world"]);
});
