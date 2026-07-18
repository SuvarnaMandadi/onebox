// onebox Resume Autofill — popup script. Talks to the background service
// worker via messages; never calls the onebox API directly (keeps the
// token handling in one place).

function sendMessage(msg) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendMessage(msg, (resp) => {
      if (chrome.runtime.lastError) {
        reject(new Error(chrome.runtime.lastError.message));
        return;
      }
      if (!resp || !resp.ok) {
        reject(new Error((resp && resp.error) || "unknown error"));
        return;
      }
      resolve(resp);
    });
  });
}

const authView = document.getElementById("authView");
const appView = document.getElementById("appView");
const serverUrlInput = document.getElementById("serverUrl");
const emailInput = document.getElementById("email");
const passwordInput = document.getElementById("password");
const authStatus = document.getElementById("authStatus");
const connectedTo = document.getElementById("connectedTo");
const modelInput = document.getElementById("model");
const resumeFileInput = document.getElementById("resumeFile");
const uploadStatus = document.getElementById("uploadStatus");

function badgeFor(status) {
  const labelMap = { pending: "Pending", processing: "Processing", done: "Ready", error: "Error" };
  return `<span class="badge badge-${status}">${labelMap[status] || status}</span>`;
}

async function refresh() {
  const { state } = await sendMessage({ type: "GET_STATE" });
  serverUrlInput.value = state.serverUrl;
  modelInput.value = state.model;

  if (state.token) {
    authView.style.display = "none";
    appView.style.display = "block";
    connectedTo.textContent = "Connected to " + state.serverUrl;
    if (state.resumeFilename) {
      uploadStatus.innerHTML = `"${state.resumeFilename}" ${badgeFor(state.resumeStatus)}`;
    }
  } else {
    authView.style.display = "block";
    appView.style.display = "none";
  }
}

async function doAuth(type) {
  authStatus.textContent = "";
  authStatus.className = "status";
  try {
    await sendMessage({
      type,
      serverUrl: serverUrlInput.value.trim(),
      email: emailInput.value.trim(),
      password: passwordInput.value,
    });
    await refresh();
  } catch (e) {
    authStatus.textContent = e.message;
    authStatus.className = "status error";
  }
}

document.getElementById("loginBtn").addEventListener("click", () => doAuth("LOGIN"));
document.getElementById("signupBtn").addEventListener("click", () => doAuth("SIGNUP"));

document.getElementById("logoutBtn").addEventListener("click", async () => {
  await sendMessage({ type: "LOGOUT" });
  await refresh();
});

modelInput.addEventListener("change", () => {
  sendMessage({ type: "SET_MODEL", model: modelInput.value.trim() });
});

document.getElementById("uploadBtn").addEventListener("click", async () => {
  const file = resumeFileInput.files[0];
  if (!file) return;
  uploadStatus.textContent = "Uploading...";
  uploadStatus.className = "status";
  try {
    const dataUrl = await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result);
      reader.onerror = reject;
      reader.readAsDataURL(file);
    });
    const { source } = await sendMessage({ type: "UPLOAD_RESUME", fileDataUrl: dataUrl, filename: file.name });
    uploadStatus.innerHTML = `"${source.filename}" ${badgeFor(source.status)}`;
    // Poll the popup's own view a few times while background.js polls the
    // server — the popup is short-lived, so this is best-effort; the
    // filename/status also refresh next time the popup opens.
    for (let i = 0; i < 15; i++) {
      await new Promise((r) => setTimeout(r, 1000));
      const { state } = await sendMessage({ type: "GET_STATE" });
      uploadStatus.innerHTML = `"${state.resumeFilename}" ${badgeFor(state.resumeStatus)}`;
      if (state.resumeStatus === "done" || state.resumeStatus === "error") break;
    }
  } catch (e) {
    uploadStatus.textContent = "Error: " + e.message;
    uploadStatus.className = "status error";
  }
});

refresh();
