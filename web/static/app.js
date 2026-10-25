"use strict";

const $ = (s) => document.querySelector(s);
const xmlEl = $("#xml"), treeEl = $("#tree"), codeEl = $("#code");
const statusEl = $("#status"), codeErrEl = $("#code-err"), hlEl = $("#hl");
let lastCode = ""; // plain text of the Go output, for copying
const closed = new Set(); // paths of collapsed nodes, kept across re-renders

let queue = Promise.resolve();
let debounce;

function genOpts() {
  return {
    envelope: $("#o-env").checked,
    namespaces: $("#o-ns").checked,
    json: $("#o-json").checked,
    jsonCase: $("#o-case").value,
    omitEmpty: $("#o-omit").checked,
    root: $("#o-root").value,
    pkg: $("#o-pkg").value,
  };
}

function setStatus(msg, bad) {
  statusEl.textContent = msg;
  statusEl.classList.toggle("bad", !!bad);
}

// Requests run one at a time, and the XML is read when the request starts,
// so an edit never races with an earlier edit's result.
function run(edit, { renderTree = true } = {}) {
  queue = queue.then(() => send(edit, renderTree)).catch((e) => setStatus(String(e), true));
  return queue;
}

async function send(edit, renderTree) {
  const res = await fetch("/api/process", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ xml: xmlEl.value, edit: edit || null, gen: genOpts() }),
  });
  const r = await res.json();

  if (r.empty) {
    treeEl.innerHTML = '<p class="empty">Paste XML to see it here. Edit values in place; changes flow back into the XML.</p>';
    treeEl.classList.remove("stale");
    codeEl.textContent = "";
    lastCode = "";
    codeErrEl.textContent = "";
    setStatus("");
    return;
  }
  if (r.error) {
    treeEl.classList.add("stale");
    setStatus(r.error, true);
    return;
  }
  treeEl.classList.remove("stale");
  setStatus("Valid XML");
  if (edit) setXML(r.xml);
  if (renderTree) {
    treeEl.innerHTML = r.tree;
    treeEl.querySelectorAll(".node").forEach((n) => {
      if (closed.has(n.dataset.path)) n.classList.add("closed");
    });
  }
  lastCode = r.code || "";
  codeEl.innerHTML = r.codeHtml || ""; // server-escaped

  codeErrEl.textContent = r.codeError || "";
}

xmlEl.addEventListener("input", () => {
  paintXML();
  clearTimeout(debounce);
  debounce = setTimeout(() => run(null), 250);
});

// Tree edits. Value edits keep the tree DOM (so focus stays); structural edits re-render it.
treeEl.addEventListener("change", (e) => {
  const t = e.target;
  const kind = t.dataset.k;
  const node = t.closest(".node");
  if (!kind || !node) return;
  run({ op: kind, path: node.dataset.path, index: Number(t.dataset.i || 0), value: t.value }, { renderTree: false });
});

treeEl.addEventListener("click", (e) => {
  const b = e.target.closest("button[data-act]");
  if (!b) return;
  const node = b.closest(".node");
  const act = b.dataset.act;
  if (act === "toggle") {
    const isClosed = node.classList.toggle("closed");
    if (isClosed) closed.add(node.dataset.path); else closed.delete(node.dataset.path);
    return;
  }
  if (act === "delete") closed.clear(); // paths shift after a delete or duplicate
  if (act === "dup") closed.clear();
  run({ op: act, path: node.dataset.path, index: 0, value: "" });
});

// Options only change the Go output.
$("#opts").addEventListener("input", () => {
  const json = $("#o-json").checked;
  $("#o-case").disabled = !json;
  $("#o-omit").disabled = !json;
  run(null, { renderTree: false });
});

$("#copy").addEventListener("click", async () => {
  const btn = $("#copy");
  try {
    await navigator.clipboard.writeText(lastCode);
    btn.textContent = "Copied";
  } catch {
    btn.textContent = "Copy failed";
  }
  setTimeout(() => (btn.textContent = "Copy"), 1200);
});

$("#sample").addEventListener("click", () => {
  closed.clear();
  setXML(SAMPLE);
  run(null);
});

// ---- XML syntax highlighting (overlay under the textarea) ----
const esc = (t) => t.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const span = (c, t) => `<span class="${c}">${esc(t)}</span>`;
const ENT = /&(?:[A-Za-z_][\w.-]*|#\d+|#x[0-9A-Fa-f]+);/g;
const TOKEN = new RegExp(
  [
    "(<!--[\\s\\S]*?-->)",                                  // 1 comment
    "(<!\\[CDATA\\[[\\s\\S]*?\\]\\]>)",                      // 2 cdata
    "(<\\?[\\s\\S]*?\\?>)",                                // 3 processing instruction
    "<(\\/?)([^\\s<>\\/!?]+)((?:\"[^\"]*\"|'[^']*'|[^<>\"'])*?)(\\/?)>", // 4-7 tag
  ].join("|"),
  "g"
);
const ATTR = /([^\s=]+)(\s*=\s*)("[^"]*"|'[^']*')/g;

function text(t) {
  let out = "", last = 0, m;
  ENT.lastIndex = 0;
  while ((m = ENT.exec(t))) {
    out += esc(t.slice(last, m.index)) + span("x-ent", m[0]);
    last = m.index + m[0].length;
  }
  return out + esc(t.slice(last));
}

function attrs(a) {
  let out = "", last = 0, m;
  ATTR.lastIndex = 0;
  while ((m = ATTR.exec(a))) {
    out += esc(a.slice(last, m.index)) + span("x-an", m[1]) + esc(m[2]) + span("x-av", m[3]);
    last = m.index + m[0].length;
  }
  return out + esc(a.slice(last));
}

function highlightXML(src) {
  let out = "", last = 0, m;
  TOKEN.lastIndex = 0;
  while ((m = TOKEN.exec(src))) {
    out += text(src.slice(last, m.index));
    last = m.index + m[0].length;
    if (m[1] !== undefined) out += span("x-com", m[1]);
    else if (m[2] !== undefined) out += span("x-pi", m[2]);
    else if (m[3] !== undefined) out += span("x-pi", m[3]);
    else out += span("x-br", "<" + m[4]) + span("x-tag", m[5]) + attrs(m[6]) + span("x-br", m[7] + ">");
  }
  return out + text(src.slice(last));
}

function paintXML() {
  // trailing newline keeps the last (possibly empty) line the same height as in the textarea
  hlEl.innerHTML = highlightXML(xmlEl.value) + "\n";
  syncScroll();
}
function syncScroll() {
  hlEl.scrollTop = xmlEl.scrollTop;
  hlEl.scrollLeft = xmlEl.scrollLeft;
}
function setXML(v) {
  xmlEl.value = v;
  paintXML();
}
xmlEl.addEventListener("scroll", syncScroll);

// ---- resizable panes ----
const panes = $(".panes");
const paneEls = [...panes.querySelectorAll(":scope > .pane")];
const MIN_W = 180;
const STORE = "soapviz.panes";

function applyWidths(w) {
  w.forEach((v, i) => panes.style.setProperty(`--c${i + 1}`, `${v}fr`)); // px values work as fr ratios
}
function currentWidths() {
  return paneEls.map((p) => p.getBoundingClientRect().width);
}
function saveWidths(w) {
  try { localStorage.setItem(STORE, JSON.stringify(w)); } catch {}
}
function resizeAt(i, start, dx) {
  // handle i sits between pane i and pane i+1; their sum stays constant
  const w = start.slice();
  const total = w[i] + w[i + 1];
  w[i] = Math.min(Math.max(start[i] + dx, MIN_W), total - MIN_W);
  w[i + 1] = total - w[i];
  return w;
}

try {
  const saved = JSON.parse(localStorage.getItem(STORE) || "null");
  if (Array.isArray(saved) && saved.length === 3 && saved.every((v) => v > 0)) applyWidths(saved);
} catch {}

panes.querySelectorAll(".handle").forEach((h) => {
  const i = Number(h.dataset.i);
  let startX = 0, start = [];
  h.addEventListener("pointerdown", (e) => {
    h.setPointerCapture(e.pointerId);
    startX = e.clientX;
    start = currentWidths();
    h.classList.add("drag");
    document.body.classList.add("dragging");
  });
  h.addEventListener("pointermove", (e) => {
    if (!h.hasPointerCapture(e.pointerId)) return;
    applyWidths(resizeAt(i, start, e.clientX - startX));
  });
  const end = (e) => {
    if (!h.hasPointerCapture(e.pointerId)) return;
    h.releasePointerCapture(e.pointerId);
    h.classList.remove("drag");
    document.body.classList.remove("dragging");
    saveWidths(currentWidths());
  };
  h.addEventListener("pointerup", end);
  h.addEventListener("pointercancel", end);
  h.addEventListener("keydown", (e) => {
    const step = e.shiftKey ? 80 : 20;
    const dx = e.key === "ArrowLeft" ? -step : e.key === "ArrowRight" ? step : 0;
    if (!dx) return;
    e.preventDefault();
    const w = resizeAt(i, currentWidths(), dx);
    applyWidths(w);
    saveWidths(w);
  });
  h.addEventListener("dblclick", () => { // reset to defaults
    panes.style.removeProperty("--c1");
    panes.style.removeProperty("--c2");
    panes.style.removeProperty("--c3");
    try { localStorage.removeItem(STORE); } catch {}
  });
});

const SAMPLE = `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:pay="http://example.com/payments">
  <soapenv:Header/>
  <soapenv:Body>
    <pay:GetStatementResponse status="ok">
      <pay:TransactionID>T-0001</pay:TransactionID>
      <pay:Account currency="SDG">
        <pay:MSISDN>0912345678</pay:MSISDN>
      </pay:Account>
      <pay:Item seq="1">
        <pay:Amount>10.50</pay:Amount>
        <pay:Memo>top-up</pay:Memo>
      </pay:Item>
      <pay:Item seq="2">
        <pay:Amount>3.00</pay:Amount>
      </pay:Item>
    </pay:GetStatementResponse>
  </soapenv:Body>
</soapenv:Envelope>`;
