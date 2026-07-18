// onebox Resume Autofill — content script.
// Injects a floating "Autofill with onebox" button on any page that has
// form fields, reads their labels, and fills them from the extension's
// background worker (which talks to the onebox server).

(function () {
  if (window.__oneboxAutofillInjected) return;
  window.__oneboxAutofillInjected = true;

  function labelFor(el) {
    if (el.id) {
      const label = document.querySelector(`label[for="${CSS.escape(el.id)}"]`);
      if (label && label.textContent.trim()) return label.textContent.trim();
    }
    const parentLabel = el.closest("label");
    if (parentLabel) {
      const clone = parentLabel.cloneNode(true);
      clone.querySelectorAll("input,textarea,select").forEach((n) => n.remove());
      const text = clone.textContent.trim();
      if (text) return text;
    }
    if (el.getAttribute("aria-label")) return el.getAttribute("aria-label").trim();
    if (el.placeholder) return el.placeholder.trim();
    if (el.name) return el.name.trim();
    return null;
  }

  const SKIP_TYPES = new Set(["submit", "button", "hidden", "password", "checkbox", "radio", "file", "image", "reset"]);

  function findFillableFields() {
    const candidates = Array.from(document.querySelectorAll("input, textarea"));
    const fields = [];
    for (const el of candidates) {
      if (el.tagName === "INPUT" && SKIP_TYPES.has((el.type || "text").toLowerCase())) continue;
      if (el.disabled || el.readOnly) continue;
      const rect = el.getBoundingClientRect();
      if (rect.width === 0 && rect.height === 0) continue;
      const label = labelFor(el);
      if (!label) continue;
      fields.push({ el, label });
    }
    return fields;
  }

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

  function fillField(el, value) {
    if (value === undefined || value === null || value === "") return;
    const proto = el.tagName === "TEXTAREA" ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(proto, "value").set;
    setter.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    el.dispatchEvent(new Event("change", { bubbles: true }));
  }

  // -- floating button, isolated in a shadow root so this content script
  // never leaks styles into (or takes styles from) the host page --

  function buildButton() {
    const host = document.createElement("div");
    host.id = "onebox-autofill-host";
    host.style.cssText = "position:fixed;bottom:20px;right:20px;z-index:2147483647;";
    const root = host.attachShadow({ mode: "open" });

    const style = document.createElement("style");
    style.textContent = `
      button {
        font: 600 14px system-ui, -apple-system, "Segoe UI", sans-serif;
        background: #2a78d6;
        color: #fff;
        border: none;
        border-radius: 999px;
        padding: 12px 20px;
        box-shadow: 0 4px 16px rgba(0,0,0,0.25);
        cursor: pointer;
        display: flex;
        align-items: center;
        gap: 8px;
      }
      button:hover { filter: brightness(1.08); }
      button:disabled { opacity: 0.6; cursor: default; }
      .status {
        font: 500 12px system-ui, sans-serif;
        color: #fff;
        background: rgba(20,20,20,0.9);
        border-radius: 8px;
        padding: 6px 10px;
        margin-bottom: 8px;
        max-width: 260px;
      }
      .wrap { display: flex; flex-direction: column; align-items: flex-end; }
      .spinner {
        width: 12px; height: 12px; border-radius: 50%;
        border: 2px solid currentColor; border-right-color: transparent;
        animation: spin 0.6s linear infinite;
      }
      @keyframes spin { to { transform: rotate(360deg); } }
    `;
    root.appendChild(style);

    const wrap = document.createElement("div");
    wrap.className = "wrap";
    const btn = document.createElement("button");
    btn.textContent = "✨ Autofill with onebox";
    wrap.appendChild(btn);
    root.appendChild(wrap);

    btn.addEventListener("click", async () => {
      const fields = findFillableFields();
      if (fields.length === 0) {
        showStatus(wrap, "No fillable fields found on this page.");
        return;
      }
      btn.disabled = true;
      const originalText = btn.textContent;
      btn.innerHTML = "";
      const spinner = document.createElement("span");
      spinner.className = "spinner";
      btn.appendChild(spinner);
      btn.appendChild(document.createTextNode(" Filling..."));

      try {
        const labels = fields.map((f) => f.label);
        const resp = await sendMessage({ type: "AUTOFILL", fields: labels });
        let filled = 0;
        for (const f of fields) {
          const value = resp.values[f.label];
          if (value) {
            fillField(f.el, value);
            filled++;
          }
        }
        showStatus(wrap, filled > 0 ? `Filled ${filled} of ${fields.length} fields.` : "No matching resume data found for these fields.");
      } catch (e) {
        showStatus(wrap, "Error: " + e.message);
      } finally {
        btn.disabled = false;
        btn.textContent = originalText;
      }
    });

    document.documentElement.appendChild(host);
  }

  function showStatus(wrap, text) {
    const existing = wrap.querySelector(".status");
    if (existing) existing.remove();
    const status = document.createElement("div");
    status.className = "status";
    status.textContent = text;
    wrap.insertBefore(status, wrap.firstChild);
    setTimeout(() => status.remove(), 5000);
  }

  if (findFillableFields().length > 0) {
    buildButton();
  }
})();
