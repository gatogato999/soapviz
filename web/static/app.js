"use strict";

const $ = (s) => document.querySelector(s);
const xmlEl = $("#xml"), treeEl = $("#tree"), codeEl = $("#code");
const statusEl = $("#status"), codeErrEl = $("#code-err"), hlEl = $("#hl");
const panes = $(".panes");
let lastCode = ""; // plain text of the Go output, for copying

// ---------------------------------------------------------------------------
// Preferences. The server only supplies defaults (config.ini). Whatever a
// developer picks is saved in their own browser, so many people can share one
// server with different themes, options and pane widths.
// ---------------------------------------------------------------------------
const PREFS_KEY = "soapviz.prefs";
const PANES_KEY = "soapviz.panes";
const THEMES = ["light", "dim", "dark"];
const CASES = ["none", "snake", "camel"];
const FALLBACK = {
  theme: "dim",
  gen: { envelope: false, namespaces: false, json: false, jsonCase: "snake", omitEmpty: false, root: "", pkg: "" },
};
let defaults = FALLBACK;

function readSaved() {
  try {
    const p = JSON.parse(localStorage.getItem(PREFS_KEY) || "null");
    return p && typeof p === "object" ? p : {};
  } catch {
    return {};
  }
}

// Saved values win, but only if they are valid; anything else falls back to the default.
function effective(base, saved) {
  const out = { theme: base.theme, gen: { ...base.gen } };
  if (THEMES.includes(saved.theme)) out.theme = saved.theme;
  const g = saved.gen || {};
  for (const k of ["envelope", "namespaces", "json", "omitEmpty"]) if (typeof g[k] === "boolean") out.gen[k] = g[k];
  if (CASES.includes(g.jsonCase)) out.gen.jsonCase = g.jsonCase;
  for (const k of ["root", "pkg"]) if (typeof g[k] === "string") out.gen[k] = g[k];
  return out;
}

function currentTheme() {
  return document.documentElement.dataset.theme;
}

function savePrefs() {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify({ theme: currentTheme(), gen: genOpts() }));
  } catch {}
}

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

function showTheme(t) {
  document.documentElement.dataset.theme = t;
  document.querySelectorAll(".sw").forEach((b) => b.setAttribute("aria-checked", String(b.dataset.theme === t)));
}

function showGen(g) {
  $("#o-env").checked = g.envelope;
  $("#o-ns").checked = g.namespaces;
  $("#o-json").checked = g.json;
  $("#o-case").value = g.jsonCase;
  $("#o-omit").checked = g.omitEmpty;
  $("#o-root").value = g.root;
  $("#o-pkg").value = g.pkg;
  syncJSONControls();
}

function syncJSONControls() {
  const json = $("#o-json").checked;
  $("#o-case").disabled = !json;
  $("#o-omit").disabled = !json;
}

function setStatus(msg, bad) {
  statusEl.textContent = msg;
  statusEl.classList.toggle("bad", !!bad);
}

// ---------------------------------------------------------------------------
// Talking to the server. Requests run one at a time, and the XML is read when
// a request starts, so an edit never races with an earlier edit's result.
// ---------------------------------------------------------------------------
let queue = Promise.resolve();
let debounce;

function run(edit, { renderTree = true } = {}) {
  queue = queue.then(() => send(edit, renderTree)).catch((e) => setStatus(String(e), true));
  return queue;
}

const EMPTY_TREE = '<p class="empty">Paste XML to see it here. Edit values in place; changes flow back into the XML.</p>';
const closed = new Set(); // paths of collapsed nodes, kept across re-renders

// By default requests go to the Go server. The static (GitHub Pages) build
// defines window.soapvizTransport, which runs the same engine as WebAssembly.
async function httpTransport(req) {
  const res = await fetch("api/process", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  return res.json();
}
const transport = window.soapvizTransport || httpTransport;

async function send(edit, renderTree) {
  const r = await transport({ xml: xmlEl.value, edit: edit || null, gen: genOpts() });

  if (r.empty) {
    treeEl.innerHTML = EMPTY_TREE;
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
    treeEl.replaceChildren(renderNode(r.tree));
  }
  lastCode = r.code || "";
  codeEl.innerHTML = r.codeHtml || ""; // escaped by the server
  codeErrEl.textContent = r.codeError || "";
}

// ---------------------------------------------------------------------------
// Tree rendering. Built with DOM APIs (never innerHTML), so XML content can
// not inject markup.
// ---------------------------------------------------------------------------
const clamp = (v, lo, hi) => Math.max(lo, Math.min(v, hi));

function h(tag, cls, props) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (props) Object.assign(e, props);
  return e;
}

function field(tag, cls, value, data, label) {
  const e = h(tag, cls);
  e.value = value;
  e.spellcheck = false;
  if (label) e.setAttribute("aria-label", label);
  for (const [k, v] of Object.entries(data)) e.dataset[k] = v;
  return e;
}

function renderNode(n) {
  const label = n.prefix ? `${n.prefix}:${n.local}` : n.local;
  const node = h("div", "node" + (n.leaf ? " leaf" : ""));
  node.dataset.path = n.path;
  if (closed.has(n.path)) node.classList.add("closed");

  const row = h("div", "row");
  if (n.leaf) {
    row.append(h("span", "tg"));
  } else {
    const t = h("button", "tg", { type: "button" });
    t.dataset.act = "toggle";
    t.setAttribute("aria-label", `Collapse or expand ${label}`);
    row.append(t);
  }

  const qn = h("span", "qn");
  if (n.prefix) qn.append(h("span", "pfx", { textContent: n.prefix + ":" }));
  const name = field("input", "nm", n.local, { k: "name", prefix: n.prefix || "" }, "Element name");
  name.size = clamp(n.local.length + 1, 3, 60);
  qn.append(name);
  row.append(qn);

  for (const ns of n.ns || []) row.append(h("span", "ns", { textContent: ns.decl, title: ns.uri }));

  for (const a of n.attrs || []) {
    const lab = h("label", "attr");
    lab.append(h("span", "an", { textContent: a.name }));
    const inp = field("input", "", a.value, { k: "attr", i: a.index });
    inp.size = clamp(a.value.length + 1, 4, 40);
    lab.append(inp);
    row.append(lab);
  }

  const text = n.text || "";
  const textLabel = `Text of ${label}`;
  if (n.leaf && text.includes("\n")) {
    const ta = field("textarea", "txt", text, { k: "text" }, textLabel);
    ta.rows = Math.min(text.split("\n").length, 8);
    row.append(ta);
  } else if (n.leaf || text) {
    const inp = field("input", "txt", text, { k: "text" }, textLabel);
    if (n.leaf) inp.placeholder = "empty";
    row.append(inp);
  }

  if (!n.root) {
    const acts = h("span", "acts");
    for (const [act, caption] of [["dup", "Duplicate"], ["delete", "Delete"]]) {
      const b = h("button", "", { type: "button", textContent: caption });
      b.dataset.act = act;
      acts.append(b);
    }
    row.append(acts);
  }
  node.append(row);

  if (n.children && n.children.length) {
    const kids = h("div", "kids");
    for (const c of n.children) kids.append(renderNode(c));
    node.append(kids);
  }
  return node;
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
  let value = t.value;
  if (kind === "name" && t.dataset.prefix) value = t.dataset.prefix + ":" + value; // prefix is shown separately
  run({ op: kind, path: node.dataset.path, index: Number(t.dataset.i || 0), value }, { renderTree: false });
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
  closed.clear(); // paths shift after a delete or duplicate
  run({ op: act, path: node.dataset.path, index: 0, value: "" });
});

// Options only change the Go output.
$("#opts").addEventListener("input", () => {
  syncJSONControls();
  savePrefs();
  run(null, { renderTree: false });
});

document.querySelectorAll(".sw").forEach((b) =>
  b.addEventListener("click", () => {
    showTheme(b.dataset.theme);
    savePrefs();
  })
);

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

$("#reset").addEventListener("click", () => {
  try {
    localStorage.removeItem(PREFS_KEY);
    localStorage.removeItem(PANES_KEY);
  } catch {}
  showTheme(defaults.theme);
  showGen(defaults.gen);
  ["--c1", "--c2", "--c3"].forEach((v) => panes.style.removeProperty(v));
  if (xmlEl.value.trim()) run(null, { renderTree: false });
});

// ---------------------------------------------------------------------------
// XML syntax highlighting: a transparent textarea over a highlighted copy.
// ---------------------------------------------------------------------------
const esc = (t) => t.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const span = (c, t) => `<span class="${c}">${esc(t)}</span>`;
const ENT = /&(?:[A-Za-z_][\w.-]*|#\d+|#x[0-9A-Fa-f]+);/g;
// groups: 1 comment, 2 cdata, 3 processing instruction, 4 "/" 5 name 6 attributes 7 "/" of a self-closing tag
const TOKEN = /(<!--[\s\S]*?-->)|(<!\[CDATA\[[\s\S]*?\]\]>)|(<\?[\s\S]*?\?>)|<(\/?)([^\s<>\/!?]+)((?:"[^"]*"|'[^']*'|[^<>"'])*?)(\/?)>/g;
const ATTR = /([^\s=]+)(\s*=\s*)("[^"]*"|'[^']*')/g;

function text(t) {
  const plain = (s) => (s.trim() ? span("x-txt", s) : esc(s));
  let out = "", last = 0, m;
  ENT.lastIndex = 0;
  while ((m = ENT.exec(t))) {
    out += plain(t.slice(last, m.index)) + span("x-ent", m[0]);
    last = m.index + m[0].length;
  }
  return out + plain(t.slice(last));
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

function tagName(n) {
  const i = n.indexOf(":");
  return i > 0 ? span("x-pfx", n.slice(0, i + 1)) + span("x-tag", n.slice(i + 1)) : span("x-tag", n);
}

function highlightXML(src) {
  let out = "", last = 0, m;
  TOKEN.lastIndex = 0;
  while ((m = TOKEN.exec(src))) {
    out += text(src.slice(last, m.index));
    last = m.index + m[0].length;
    if (m[1] !== undefined) out += span("x-com", m[1]);
    else if (m[2] !== undefined || m[3] !== undefined) out += span("x-pi", m[2] ?? m[3]);
    else out += span("x-br", "<" + m[4]) + tagName(m[5]) + attrs(m[6]) + span("x-br", m[7] + ">");
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

// ---------------------------------------------------------------------------
// Resizable panes
// ---------------------------------------------------------------------------
const paneEls = [...panes.querySelectorAll(":scope > .pane")];
const MIN_W = 180;

function applyWidths(w) {
  w.forEach((v, i) => panes.style.setProperty(`--c${i + 1}`, `${v}fr`)); // px values work as fr ratios
}
function currentWidths() {
  return paneEls.map((p) => p.getBoundingClientRect().width);
}
function saveWidths(w) {
  try { localStorage.setItem(PANES_KEY, JSON.stringify(w)); } catch {}
}
function resizeAt(i, start, dx) {
  // handle i sits between pane i and pane i+1; their sum stays constant
  const w = start.slice();
  const total = w[i] + w[i + 1];
  w[i] = Math.min(Math.max(start[i] + dx, MIN_W), total - MIN_W);
  w[i + 1] = total - w[i];
  return w;
}

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
  h.addEventListener("dblclick", () => {
    ["--c1", "--c2", "--c3"].forEach((v) => panes.style.removeProperty(v));
    try { localStorage.removeItem(PANES_KEY); } catch {}
  });
});

// ---------------------------------------------------------------------------
// Startup: server defaults, then this browser's saved choices on top.
// ---------------------------------------------------------------------------
async function init() {
  if (!window.soapvizTransport) { // the static build has no server to ask
    try {
      const r = await fetch("api/config");
      if (r.ok) defaults = effective(FALLBACK, await r.json());
    } catch {}
  }
  const prefs = effective(defaults, readSaved());
  showTheme(prefs.theme);
  showGen(prefs.gen);
  try {
    const saved = JSON.parse(localStorage.getItem(PANES_KEY) || "null");
    if (Array.isArray(saved) && saved.length === 3 && saved.every((v) => v > 0)) applyWidths(saved);
  } catch {}
  paintXML();
}
init();

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
        <pay:Memo>top-up &amp; fees</pay:Memo>
      </pay:Item>
      <pay:Item seq="2">
        <pay:Amount>3.00</pay:Amount>
      </pay:Item>
    </pay:GetStatementResponse>
  </soapenv:Body>
</soapenv:Envelope>`;
