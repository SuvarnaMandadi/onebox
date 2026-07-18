// onebox Resume Autofill — background service worker.
// Holds the connection (server URL + session token) in chrome.storage.local
// and does every onebox API call, so the content script injected into an
// arbitrary web page never touches credentials directly — it only ever
// sends/receives plain messages.

const DEFAULTS = {
  serverUrl: "http://localhost:8090",
  token: "",
  model: "claude-sonnet-5",
  resumeFilename: "",
  resumeStatus: "",
};

async function getState() {
  const stored = await chrome.storage.local.get(DEFAULTS);
  return stored;
}

async function setState(patch) {
  await chrome.storage.local.set(patch);
}

async function apiCall(serverUrl, path, opts = {}) {
  const headers = new Headers(opts.headers || {});
  const state = await getState();
  if (state.token) headers.set("Authorization", "Bearer " + state.token);
  if (opts.body && !(opts.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(serverUrl.replace(/\/+$/, "") + path, { ...opts, headers });
  const isJSON = (res.headers.get("content-type") || "").includes("application/json");
  const body = isJSON ? await res.json() : await res.text();
  if (!res.ok) {
    const msg = isJSON && body && body.message ? body.message : String(body);
    throw new Error(msg);
  }
  return body;
}

async function handleAuth(kind, serverUrl, email, password) {
  const resp = await apiCall(serverUrl, "/api/auth/" + kind, {
    method: "POST",
    body: JSON.stringify({ email, password }),
  });
  await setState({ serverUrl, token: resp.token });
  return resp;
}

async function handleUploadResume(serverUrl, fileDataUrl, filename) {
  // FormData/Blob work fine in an MV3 service worker; the popup can't
  // hand us a raw File across the message-passing boundary, so it sends
  // a data URL and we reconstruct the Blob here.
  const res = await fetch(fileDataUrl);
  const blob = await res.blob();
  const form = new FormData();
  form.append("file", blob, filename);
  const src = await apiCall(serverUrl, "/api/rag/sources", { method: "POST", body: form });
  await setState({ resumeFilename: filename, resumeStatus: src.status, resumeSourceId: src.id });
  pollResumeStatus(serverUrl, src.id);
  return src;
}

async function pollResumeStatus(serverUrl, id) {
  for (let i = 0; i < 30; i++) {
    await new Promise((r) => setTimeout(r, 1000));
    let src;
    try {
      src = await apiCall(serverUrl, "/api/rag/sources/" + id);
    } catch (e) {
      return;
    }
    await setState({ resumeStatus: src.status });
    if (src.status === "done" || src.status === "error") return;
  }
}

// extractFieldsFromResume asks onebox for the resume content relevant to
// the requested fields (via /api/rag/query, the retrieval half of RAG),
// then asks the LLM gateway to return a strict JSON mapping of field ->
// value grounded in that content. This is the "or /api/llm/chat grounded
// in the resume" path: a custom two-step RAG answer instead of
// /api/rag/answer, because we need structured JSON output to fill form
// fields programmatically, not a prose paragraph.
async function extractFieldsFromResume(serverUrl, model, fields) {
  const query = await apiCall(serverUrl, "/api/rag/query", {
    method: "POST",
    body: JSON.stringify({ query: fields.join(", "), top_k: 8 }),
  });
  const context = (query.results || []).map((r) => r.text).join("\n\n");
  if (!context) {
    throw new Error("No resume content found — upload a resume first.");
  }

  const exampleObj = {};
  for (const f of fields) exampleObj[f] = "example value as a plain string";

  const chat = await apiCall(serverUrl, "/api/llm/chat", {
    method: "POST",
    body: JSON.stringify({
      model,
      messages: [
        {
          role: "system",
          content:
            "You extract structured data from a resume to fill a web form. " +
            "Respond with ONLY a single JSON object, no markdown fences, no explanation. " +
            "Every value must be a single plain string — never a nested object or an array. " +
            "If a field has multiple items (e.g. several jobs or skills), join them into one " +
            "string separated by '; '. Use an empty string for any field not found in the resume. " +
            "Example shape (values are illustrative, not real):\n" +
            JSON.stringify(exampleObj),
        },
        {
          role: "user",
          content:
            "Resume content:\n\n" +
            context +
            "\n\nExtract these fields as a JSON object with exactly these keys: " +
            JSON.stringify(fields) +
            "\n\nRespond with only the JSON object, matching the example shape exactly.",
        },
      ],
    }),
  });

  return parseModelJSON(chat.content);
}

// parseModelJSON is defensive on purpose: small/local models don't reliably
// follow "respond with only JSON" instructions, and this is a demo meant
// to work across whatever provider/model the user has configured, not
// just well-behaved large hosted models. It tries, in order: (1) the
// response as-is, (2) stripping markdown code fences, (3) trimming to the
// outermost {...} in case the model added leading/trailing prose despite
// instructions. Whatever survives parsing is then normalized so every
// field value is a plain string — a nested object/array (also a known
// small-model failure mode, seen in testing with llama3.2:1b) is
// flattened into a readable string instead of breaking the fill step.
function parseModelJSON(text) {
  const attempts = [
    text.trim(),
    text.trim().replace(/^```(?:json)?\s*/i, "").replace(/```\s*$/, ""),
  ];
  const firstBrace = text.indexOf("{");
  const lastBrace = text.lastIndexOf("}");
  if (firstBrace !== -1 && lastBrace > firstBrace) {
    attempts.push(text.slice(firstBrace, lastBrace + 1));
  }

  let parsed = null;
  let lastError = null;
  for (const attempt of attempts) {
    try {
      parsed = JSON.parse(attempt);
      break;
    } catch (e) {
      lastError = e;
    }
  }
  if (parsed === null) {
    throw new Error("Model did not return valid JSON (" + lastError.message + "): " + text.slice(0, 200));
  }

  const flattened = {};
  for (const [key, value] of Object.entries(parsed)) {
    flattened[key] = flattenValue(value);
  }
  return flattened;
}

function flattenValue(value) {
  if (value === null || value === undefined) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.map(flattenValue).filter(Boolean).join("; ");
  if (typeof value === "object") {
    // A nested object with no useful keys (e.g. `{"some string": []}`,
    // seen from llama3.2:1b) — fall back to its own keys as the content,
    // since that's usually where the model actually put the real text.
    return Object.entries(value)
      .map(([k, v]) => {
        const flat = flattenValue(v);
        return flat ? k + ": " + flat : k;
      })
      .filter(Boolean)
      .join("; ");
  }
  return String(value);
}

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  (async () => {
    try {
      switch (msg.type) {
        case "GET_STATE":
          sendResponse({ ok: true, state: await getState() });
          break;
        case "SET_SERVER":
          await setState({ serverUrl: msg.serverUrl });
          sendResponse({ ok: true });
          break;
        case "SET_MODEL":
          await setState({ model: msg.model });
          sendResponse({ ok: true });
          break;
        case "LOGIN":
        case "SIGNUP":
          sendResponse({ ok: true, resp: await handleAuth(msg.type === "LOGIN" ? "login" : "signup", msg.serverUrl, msg.email, msg.password) });
          break;
        case "LOGOUT":
          await setState({ token: "", resumeFilename: "", resumeStatus: "", resumeSourceId: "" });
          sendResponse({ ok: true });
          break;
        case "UPLOAD_RESUME": {
          const state = await getState();
          sendResponse({ ok: true, source: await handleUploadResume(state.serverUrl, msg.fileDataUrl, msg.filename) });
          break;
        }
        case "AUTOFILL": {
          const state = await getState();
          const values = await extractFieldsFromResume(state.serverUrl, state.model, msg.fields);
          sendResponse({ ok: true, values });
          break;
        }
        default:
          sendResponse({ ok: false, error: "unknown message type: " + msg.type });
      }
    } catch (e) {
      sendResponse({ ok: false, error: e.message });
    }
  })();
  return true; // keep the message channel open for the async response
});
