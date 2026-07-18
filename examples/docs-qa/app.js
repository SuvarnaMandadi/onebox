// onebox example: docs Q&A. Plain fetch(), no SDK/build step — see
// docs/tutorial-chat-with-your-docs.md for the same flow using the JS SDK.
const BASE_URL = "http://localhost:8090";
const TOKEN_KEY = "onebox_docsqa_token";

let currentSourceId = null;

async function api(path, opts = {}) {
  const token = localStorage.getItem(TOKEN_KEY);
  const headers = new Headers(opts.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (opts.body && !(opts.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(BASE_URL + path, { ...opts, headers });
  const isJSON = (res.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await res.json() : await res.text();
  if (!res.ok) throw new Error((isJSON && body.message) || String(body));
  return body;
}

// Every upload/query needs a onebox identity to own it. A real app would
// show a signup/login form; this demo just creates a throwaway account
// once and remembers its token.
async function ensureSession() {
  if (localStorage.getItem(TOKEN_KEY)) return;
  const email = `visitor-${Date.now()}@example.com`;
  const password = crypto.randomUUID();
  const { token } = await api("/api/auth/signup", { method: "POST", body: JSON.stringify({ email, password }) });
  localStorage.setItem(TOKEN_KEY, token);
}

async function waitForIngestion(id, onStatus) {
  for (;;) {
    const source = await api(`/api/rag/sources/${id}`);
    onStatus(source.status);
    if (source.status === "done") return source;
    if (source.status === "error") throw new Error(source.error || "ingestion failed");
    await new Promise((r) => setTimeout(r, 1000));
  }
}

const uploadStatus = document.getElementById("uploadStatus");
const fileInput = document.getElementById("fileInput");
const questionInput = document.getElementById("questionInput");
const askBtn = document.getElementById("askBtn");
const answerEl = document.getElementById("answer");
const sourcesEl = document.getElementById("sources");

fileInput.addEventListener("change", async () => {
  const file = fileInput.files[0];
  if (!file) return;
  try {
    await ensureSession();
    uploadStatus.textContent = "uploading...";
    const form = new FormData();
    form.append("file", file);
    const source = await api("/api/rag/sources", { method: "POST", body: form });
    currentSourceId = source.id;
    uploadStatus.textContent = "ingesting (extracting, chunking, embedding)...";
    await waitForIngestion(source.id, (status) => {
      uploadStatus.textContent = `status: ${status}`;
    });
    uploadStatus.textContent = `ready — "${file.name}" ingested, ask a question below`;
  } catch (e) {
    uploadStatus.textContent = "error: " + e.message;
  }
});

askBtn.addEventListener("click", async () => {
  const query = questionInput.value.trim();
  if (!query) return;
  answerEl.textContent = "thinking...";
  sourcesEl.textContent = "";
  try {
    await ensureSession();
    const { answer, sources } = await api("/api/rag/answer", {
      method: "POST",
      body: JSON.stringify({ query, top_k: 5 }),
    });
    answerEl.textContent = answer;
    sourcesEl.textContent = sources.length
      ? "Sources:\n" + sources.map((s) => `[${s.score.toFixed(2)}] ${s.text.slice(0, 100)}...`).join("\n")
      : "";
  } catch (e) {
    answerEl.textContent = "error: " + e.message;
  }
});
