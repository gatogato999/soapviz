"use strict";

const $ = (s) => document.querySelector(s);
const xmlEl = $("#xml"), treeEl = $("#tree"), codeEl = $("#code");
const statusEl = $("#status"), codeErrEl = $("#code-err");
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
  if (edit) xmlEl.value = r.xml;
  if (renderTree) {
    treeEl.innerHTML = r.tree;
    treeEl.querySelectorAll(".node").forEach((n) => {
      if (closed.has(n.dataset.path)) n.classList.add("closed");
    });
  }
  codeEl.textContent = r.code || "";
  codeErrEl.textContent = r.codeError || "";
}

xmlEl.addEventListener("input", () => {
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
    await navigator.clipboard.writeText(codeEl.textContent);
    btn.textContent = "Copied";
  } catch {
    btn.textContent = "Copy failed";
  }
  setTimeout(() => (btn.textContent = "Copy"), 1200);
});

$("#sample").addEventListener("click", () => {
  closed.clear();
  xmlEl.value = SAMPLE;
  run(null);
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
