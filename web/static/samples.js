"use strict";
// Samples UI: save, switch, rename, delete, export/import, and the team library.
// "My samples" live in this browser (samples-store.js). "Team samples" live in a
// folder on the server and exist only when the server enables them.
// Relies on app.js globals at call time (not at load): setXML, run, closed.
(() => {
  const Tags = window.soapvizTags;
  const Store = window.soapvizStore;
  const q = (s) => document.querySelector(s);
  const xmlTA = q("#xml"); // samples.js loads before app.js, so it can't use app.js's xmlEl at load time

  const LAST_KEY = "soapviz.last"; // id of the last open "my sample"
  const MAX_NAME = 80;
  const MAX_BYTES = 8 * 1024 * 1024; // matches the server's default request limit
  const MAX_IMPORT = 500;

  // ---- state ----
  const S = {
    shared: { enabled: false, writable: false },
    active: null,   // {where: "local"|"shared", key, name, tag, baseline, updatedAt}
    previous: null, // {where, key}: what Ctrl+K then Enter returns to
    local: [], team: [], teamError: "", teamLoaded: false,
    rows: [], hl: -1,
  };

  // ---- validation (mirrors shared.ValidateName / ValidateTag in Go; the server is authoritative) ----
  const BAD_NAME = /[\x00-\x1f\x7f/\\:*?"<>|[\]]/;
  function nameError(n) {
    if (n === "" || n !== n.trim()) return "Enter a name (no spaces at the start or end).";
    if ([...n].length > MAX_NAME) return `Names are at most ${MAX_NAME} characters.`;
    if (n[0] === ".") return "A name can't start with a dot.";
    if (BAD_NAME.test(n)) return 'A name can\'t contain / \\ : * ? " < > | [ ] or control characters.';
    return "";
  }
  const tagError = (t) => (Tags.valid(t) ? "" : "A tag is 1 to 4 letters.");

  // ---- small helpers ----
  function toast(msg, bad) {
    const t = q("#toast");
    t.textContent = msg;
    t.classList.toggle("bad", !!bad);
    t.hidden = false;
    clearTimeout(toast.timer);
    toast.timer = setTimeout(() => { t.hidden = true; }, bad ? 6000 : 2500);
  }

  function formatBytes(n) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
    return `${(n / 1048576).toFixed(1)} MB`;
  }

  function ago(ms) {
    const s = Math.max(0, (Date.now() - ms) / 1000);
    if (s < 60) return "just now";
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    if (s < 86400 * 30) return `${Math.floor(s / 86400)} d ago`;
    return new Date(ms).toLocaleDateString();
  }

  function download(filename, text) {
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([text], { type: "application/json" }));
    a.download = filename;
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }

  function uniqueName(base, taken) {
    const lower = new Set(taken.map((n) => n.toLowerCase()));
    if (!lower.has(base.toLowerCase())) return base;
    for (let i = 2; ; i++) {
      const suffix = ` (${i})`;
      const cand = [...base].slice(0, MAX_NAME - suffix.length).join("").trimEnd() + suffix;
      if (!lower.has(cand.toLowerCase())) return cand;
    }
  }

  // ---- team samples API ----
  async function teamFetch(method, name, body) {
    const url = name == null ? "api/shared" : "api/shared/sample?name=" + encodeURIComponent(name);
    const res = await fetch(url, {
      method,
      headers: body ? { "Content-Type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    let data = null;
    if (res.status !== 204) { try { data = await res.json(); } catch { /* not JSON */ } }
    if (!res.ok) {
      throw Object.assign(new Error((data && data.error) || `Server error (${res.status})`), {
        code: data && data.code, updatedAt: data && data.updatedAt, status: res.status,
      });
    }
    return data;
  }

  // ---- dialogs (native <dialog>) ----
  function whenClosed(d, fallback) {
    return new Promise((resolve) => {
      d.returnValue = fallback;
      d.addEventListener("close", () => resolve(d.returnValue), { once: true });
      d.showModal();
    });
  }

  async function askConfirm(title, body, okLabel, danger) {
    q("#dlg-confirm-title").textContent = title;
    q("#dlg-confirm-body").textContent = body;
    const ok = q("#dlg-confirm-ok");
    ok.textContent = okLabel || "OK";
    ok.classList.toggle("danger", !!danger);
    return (await whenClosed(q("#dlg-confirm"), "cancel")) === "ok";
  }

  async function askUnsaved(name) {
    q("#dlg-unsaved-body").textContent = name
      ? `"${name}" has changes that are not saved.`
      : "You have XML that is not saved as a sample.";
    return whenClosed(q("#dlg-unsaved"), "cancel"); // save | discard | cancel
  }

  // Resolves to {name, tag, where} or null. opts: title, name, tag, where, lockWhere, note, error
  function askSave(opts) {
    const d = q("#dlg-save"), nameEl = q("#save-name"), tagEl = q("#save-tag");
    q("#dlg-save-title").textContent = opts.title;
    nameEl.value = opts.name || "";
    tagEl.value = opts.tag || "";
    q("#save-error").textContent = opts.error || "";
    q("#save-note").textContent = opts.note || "";
    const teamOk = S.shared.enabled && S.shared.writable && !opts.lockWhere;
    const where = q("#save-where");
    where.hidden = !(S.shared.enabled && S.shared.writable) || !!opts.lockWhere;
    q("#save-where-team").disabled = !teamOk;
    for (const r of where.querySelectorAll("input[name=where]")) r.checked = r.value === (teamOk ? opts.where || "local" : "local");
    const paintChip = () => {
      const slot = q("#save-chip");
      slot.replaceChildren();
      if (Tags.valid(tagEl.value) && tagEl.value) slot.append(Tags.chip(tagEl.value));
    };
    paintChip();
    tagEl.oninput = paintChip;
    return whenClosed(d, "cancel").then((v) => {
      if (v !== "ok") return null;
      const chosen = where.querySelector("input[name=where]:checked");
      return { name: nameEl.value, tag: tagEl.value, where: chosen && !where.hidden ? chosen.value : opts.lockWhere || "local" };
    });
  }

  q("#save-form").addEventListener("submit", (e) => {
    if (e.submitter && e.submitter.value === "cancel") return;
    const err = nameError(q("#save-name").value) || tagError(q("#save-tag").value);
    if (err) {
      e.preventDefault();
      q("#save-error").textContent = err;
    }
  });

  // ---- active sample and dirty state ----
  function isDirty() {
    if (S.active) return xmlTA.value !== S.active.baseline;
    return xmlTA.value.trim() !== "";
  }

  function renderActive() {
    const el = q("#active-sample"), a = S.active;
    el.replaceChildren();
    if (a) {
      const c = Tags.chip(a.tag);
      if (c) el.append(c);
      el.append(Object.assign(document.createElement("span"), { className: "aname", textContent: a.name }));
      if (a.where === "shared") el.append(Object.assign(document.createElement("span"), { className: "awhere", textContent: "team" }));
      if (isDirty()) {
        el.append(Object.assign(document.createElement("span"), { className: "adirty", textContent: "●", title: "Unsaved changes" }));
        el.setAttribute("aria-label", `${a.name}, unsaved changes`);
      } else {
        el.setAttribute("aria-label", a.name);
      }
    } else {
      el.removeAttribute("aria-label");
    }
    q("#save-btn").disabled = !isDirty() && !!a;
    q("#save-btn").textContent = a && a.where === "shared" && !S.shared.writable ? "Save copy" : "Save";
  }

  function setActive(a) {
    // Whatever was open becomes "previous", so Ctrl+K then Enter always toggles back.
    if (S.active && !(a && a.where === S.active.where && a.key === S.active.key)) {
      S.previous = { where: S.active.where, key: S.active.key };
    }
    S.active = a;
    try {
      if (a && a.where === "local") localStorage.setItem(LAST_KEY, a.key);
      else localStorage.removeItem(LAST_KEY);
    } catch { /* storage blocked: fine */ }
    renderActive();
  }

  // Called before anything replaces the editor text. Resolves false if the user cancels.
  async function guardReplace() {
    if (!isDirty()) return true;
    const choice = await askUnsaved(S.active ? S.active.name : null);
    if (choice === "cancel") return false;
    if (choice === "discard") return true;
    return save();
  }

  // ---- open / save ----
  async function openSample(where, key) {
    if (!(await guardReplace())) return false;
    let rec;
    try {
      rec = where === "local" ? await Store.get(key) : await teamFetch("GET", key);
    } catch (e) {
      toast(e.status === 404 ? "That sample no longer exists." : e.message, true);
      return false;
    }
    if (!rec) { toast("That sample no longer exists.", true); return false; }
    const next = where === "local"
      ? { where, key: rec.id, name: rec.name, tag: rec.tag || "", baseline: rec.xml, updatedAt: rec.updatedAt }
      : { where, key: rec.name, name: rec.name, tag: rec.tag || "", baseline: rec.xml, updatedAt: rec.updatedAt };
    setActive(next);
    closed.clear();
    setXML(rec.xml);
    run(null);
    if (where === "local") Store.touch(next.key).catch(() => {});
    return true;
  }

  async function createLocal(name, tag, xml) {
    const m = await Store.create({ name, tag, xml });
    Store.persist();
    return m;
  }

  async function persistNew(r, xml) {
    if (r.where === "shared") {
      try {
        const info = await teamFetch("PUT", r.name, { tag: r.tag, xml });
        setActive({ where: "shared", key: info.name, name: info.name, tag: info.tag || "", baseline: xml, updatedAt: info.updatedAt });
      } catch (e) {
        if (e.code === "exists") throw Object.assign(new Error("A team sample with that name already exists."), { code: "exists" });
        throw e;
      }
    } else {
      const m = await createLocal(r.name, r.tag, xml);
      setActive({ where: "local", key: m.id, name: m.name, tag: m.tag, baseline: xml, updatedAt: m.updatedAt });
    }
  }

  // Ask for a name and save the current text as a new sample.
  async function saveAsFlow(opts) {
    const xml = xmlTA.value;
    if (!xml.trim()) { toast("There is nothing to save.", true); return false; }
    if (new Blob([xml]).size > MAX_BYTES) { toast("That XML is larger than 8 MB.", true); return false; }
    let o = { title: "Save sample", ...opts };
    for (;;) {
      const r = await askSave(o);
      if (!r) return false;
      try {
        await persistNew(r, xml);
        toast(`Saved "${r.name}"`);
        return true;
      } catch (e) {
        if (e.code === "exists") { o = { ...o, ...r, error: e.message }; continue; }
        toast(e.message, true);
        return false;
      }
    }
  }

  async function save() {
    const a = S.active;
    const xml = xmlTA.value;
    if (!xml.trim()) { toast("There is nothing to save.", true); return false; }
    if (!a) return saveAsFlow({});
    if (!isDirty()) { toast("Already saved."); return true; }
    if (new Blob([xml]).size > MAX_BYTES) { toast("That XML is larger than 8 MB.", true); return false; }
    try {
      if (a.where === "local") {
        const m = await Store.update(a.key, { xml });
        if (!m) { toast("That sample was deleted elsewhere. Save it as a new one.", true); setActive(null); return saveAsFlow({ name: a.name, tag: a.tag }); }
        a.baseline = xml; a.updatedAt = m.updatedAt;
      } else if (S.shared.writable) {
        let info;
        try {
          info = await teamFetch("PUT", a.name, { tag: a.tag, xml, ifUpdatedAt: a.updatedAt });
        } catch (e) {
          if (e.code !== "conflict") throw e;
          const ok = await askConfirm("Someone else changed this sample",
            `"${a.name}" was changed on the server after you opened it. Overwrite their version with yours?`, "Overwrite", true);
          if (!ok) return false;
          info = await teamFetch("PUT", a.name, { tag: a.tag, xml, ifUpdatedAt: e.updatedAt });
        }
        a.baseline = xml; a.updatedAt = info.updatedAt;
      } else {
        return saveAsFlow({ title: "Save a copy", name: a.name, tag: a.tag, lockWhere: "local",
          note: "Team samples are read-only on this server, so this saves a copy in your browser." });
      }
    } catch (e) {
      toast(e.message, true);
      return false;
    }
    renderActive();
    toast(`Saved "${a.name}"`);
    return true;
  }

  // ---- switcher ----
  const sw = () => q("#dlg-switch");

  async function refreshLists() {
    const jobs = [];
    if (Store.available) {
      jobs.push(Store.list().then((l) => { S.local = l; }).catch((e) => { S.local = []; toast(e.message, true); }));
    }
    if (S.shared.enabled) {
      jobs.push(teamFetch("GET").then((d) => { S.team = d.samples; S.teamError = ""; })
        .catch((e) => { S.team = []; S.teamError = e.message; })
        .finally(() => { S.teamLoaded = true; }));
    }
    await Promise.all(jobs);
  }

  function matches(item, filter) {
    if (!filter) return true;
    const hay = `${item.name} ${item.tag || ""}`.toLowerCase();
    return filter.toLowerCase().split(/\s+/).filter(Boolean).every((w) => hay.includes(w));
  }

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  function actionButton(label, action, where, key) {
    const b = el("button", "", label);
    b.type = "button";
    b.dataset.a = action;
    b.dataset.where = where;
    b.dataset.key = key;
    if (action === "delete") b.classList.add("danger-hover");
    return b;
  }

  function renderList() {
    const list = q("#sw-list"), filter = q("#sw-filter").value.trim();
    list.replaceChildren();
    S.rows = [];

    const group = (title, count) => list.append(el("li", "sw-group", `${title} (${count})`));
    const note = (text, cls) => list.append(el("li", "sw-note" + (cls ? " " + cls : ""), text));

    const addRow = (where, key, item, actions) => {
      const li = el("li", "srow");
      li.setAttribute("role", "presentation");
      const opt = el("div", "sopen");
      opt.setAttribute("role", "option");
      opt.id = "opt-" + S.rows.length;
      opt.dataset.where = where;
      opt.dataset.key = key;
      const c = Tags.chip(item.tag);
      opt.append(c || el("span", "chip-gap"));
      opt.append(el("span", "sname", item.name));
      const isActive = S.active && S.active.where === where && S.active.key === key;
      if (isActive) opt.append(el("span", "sopenmark", "open"));
      opt.append(el("span", "smeta", `${formatBytes(item.bytes)} · ${ago(item.updatedAt)}`));
      const acts = el("span", "sacts");
      for (const a of actions) acts.append(actionButton(a[0], a[1], where, key));
      li.append(opt, acts);
      list.append(li);
      S.rows.push({ where, key, opt });
    };

    // My samples
    const mine = S.local.filter((s) => matches(s, filter)).sort((a, b) => b.lastUsedAt - a.lastUsedAt);
    group("My samples", mine.length);
    if (!Store.available) note("Saving in this browser is not available here (private mode or blocked storage).", "warn");
    else if (!S.local.length) note("Nothing saved yet. Paste some XML and press Ctrl+S.");
    else if (!mine.length) note("No match.");
    for (const s of mine) {
      const acts = [["Rename", "rename"], ["Export", "export"]];
      if (S.shared.writable) acts.push(["Publish", "publish"]);
      acts.push(["Delete", "delete"]);
      addRow("local", s.id, s, acts);
    }

    // Team samples
    if (S.shared.enabled) {
      const team = S.team.filter((s) => matches(s, filter));
      group("Team samples", team.length);
      if (S.teamError) note(`Could not load team samples: ${S.teamError}`, "warn");
      else if (!S.teamLoaded) note("Loading…");
      else if (!S.team.length) note(S.shared.writable ? "No team samples yet. Use Publish on one of yours." : "No team samples yet. An admin adds .xml files to the server's shared folder.");
      else if (!team.length) note("No match.");
      for (const s of team) {
        const acts = [["Copy to mine", "copy"]];
        if (S.shared.writable) acts.push(["Delete", "delete"]);
        addRow("shared", s.name, s, acts);
      }
    }

    // initial highlight: the sample you came from, else the first one that isn't open
    let hl = -1;
    if (S.previous) hl = S.rows.findIndex((r) => r.where === S.previous.where && r.key === S.previous.key);
    if (hl < 0) hl = S.rows.findIndex((r) => !(S.active && r.where === S.active.where && r.key === S.active.key));
    if (hl < 0 && S.rows.length) hl = 0;
    setHighlight(hl);
  }

  function setHighlight(i) {
    S.hl = i;
    const input = q("#sw-filter");
    S.rows.forEach((r, n) => {
      r.opt.setAttribute("aria-selected", String(n === i));
      r.opt.parentElement.classList.toggle("hl", n === i);
    });
    if (i >= 0 && S.rows[i]) {
      input.setAttribute("aria-activedescendant", S.rows[i].opt.id);
      S.rows[i].opt.scrollIntoView({ block: "nearest" });
    } else {
      input.removeAttribute("aria-activedescendant");
    }
  }

  async function openSwitcher() {
    const d = sw();
    if (d.open) return;
    q("#sw-filter").value = "";
    d.showModal();
    q("#sw-filter").focus();
    renderList(); // immediately, with whatever is cached; then refresh
    await refreshLists();
    if (d.open) renderList();
  }

  const closeSwitcher = () => { if (sw().open) sw().close(); };

  q("#sw-filter").addEventListener("input", renderList);
  q("#sw-filter").addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!S.rows.length) return;
      const step = e.key === "ArrowDown" ? 1 : -1;
      setHighlight((S.hl + step + S.rows.length) % S.rows.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const r = S.rows[S.hl];
      if (r) { closeSwitcher(); openSample(r.where, r.key); }
    }
  });

  q("#sw-list").addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-a]");
    if (btn) { await rowAction(btn.dataset.a, btn.dataset.where, btn.dataset.key); return; }
    const opt = e.target.closest(".sopen");
    if (opt) { closeSwitcher(); openSample(opt.dataset.where, opt.dataset.key); }
  });

  q("#sw-close").addEventListener("click", closeSwitcher);
  sw().addEventListener("click", (e) => { if (e.target === sw()) closeSwitcher(); }); // click on the backdrop

  // ---- row actions ----
  async function rowAction(action, where, key) {
    try {
      if (where === "local") {
        const rec = await Store.get(key);
        if (!rec) { toast("That sample no longer exists.", true); await refreshLists(); renderList(); return; }
        if (action === "rename") {
          let o = { title: "Rename sample", name: rec.name, tag: rec.tag, lockWhere: "local" };
          for (;;) {
            const r = await askSave(o);
            if (!r) return;
            try {
              await Store.update(key, { name: r.name, tag: r.tag });
              if (S.active && S.active.where === "local" && S.active.key === key) { S.active.name = r.name; S.active.tag = r.tag; renderActive(); }
              break;
            } catch (e) {
              if (e.code === "exists") { o = { ...o, ...r, error: e.message }; continue; }
              throw e;
            }
          }
        } else if (action === "export") {
          download(`${rec.name}.json`, exportJSON([rec]));
          return;
        } else if (action === "publish") {
          await publish(rec);
        } else if (action === "delete") {
          if (!(await askConfirm("Delete sample?", `"${rec.name}" will be removed from this browser. This can't be undone.`, "Delete", true))) return;
          await Store.remove(key);
          if (S.active && S.active.where === "local" && S.active.key === key) setActive(null);
          if (S.previous && S.previous.where === "local" && S.previous.key === key) S.previous = null;
        }
      } else {
        if (action === "copy") {
          const rec = await teamFetch("GET", key);
          const taken = S.local.map((s) => s.name);
          await createLocal(uniqueName(rec.name, taken), rec.tag || "", rec.xml);
          toast(`Copied "${rec.name}" to your samples`);
        } else if (action === "delete") {
          if (!(await askConfirm("Delete team sample?", `"${key}" will be removed for everyone using this server. This can't be undone.`, "Delete", true))) return;
          await teamFetch("DELETE", key);
          if (S.active && S.active.where === "shared" && S.active.key === key) setActive(null);
          if (S.previous && S.previous.where === "shared" && S.previous.key === key) S.previous = null;
        }
      }
    } catch (e) {
      toast(e.message, true);
    }
    await refreshLists();
    if (sw().open) renderList();
  }

  async function publish(rec) {
    try {
      await teamFetch("PUT", rec.name, { tag: rec.tag, xml: rec.xml });
    } catch (e) {
      if (e.code !== "exists") throw e;
      const existing = (await teamFetch("GET")).samples.find((s) => s.name.toLowerCase() === rec.name.toLowerCase());
      const ok = await askConfirm("Replace the team sample?", `The team already has "${rec.name}". Replace it with your version?`, "Replace", true);
      if (!ok || !existing) return;
      await teamFetch("PUT", existing.name, { tag: rec.tag, xml: rec.xml, ifUpdatedAt: existing.updatedAt });
    }
    toast(`Published "${rec.name}" to the team`);
  }

  // ---- export / import ----
  function exportJSON(recs) {
    return JSON.stringify({
      format: "soapviz-samples", version: 1, exportedAt: new Date().toISOString(),
      samples: recs.map((r) => ({ name: r.name, tag: r.tag || "", xml: r.xml, updatedAt: r.updatedAt })),
    }, null, 2);
  }

  const FILE_TAG_RE = /^(.*\S)\s*\[([A-Za-z]{1,4})\]$/;

  // Turns an imported file into {name, tag, xml} candidates. Anything malformed is dropped by the caller.
  async function readImport(file) {
    const text = await file.text();
    if (/\.json$/i.test(file.name)) {
      const data = JSON.parse(text);
      if (!data || data.format !== "soapviz-samples" || !Array.isArray(data.samples)) throw new Error(`${file.name} is not a soapviz export.`);
      return data.samples.map((s) => ({ name: s && s.name, tag: (s && s.tag) || "", xml: s && s.xml }));
    }
    let stem = file.name.replace(/\.[^.]*$/, "");
    let tag = "";
    const m = FILE_TAG_RE.exec(stem);
    if (m) { stem = m[1]; tag = m[2]; }
    return [{ name: stem.trim(), tag, xml: text }];
  }

  async function importFiles(files) {
    let added = 0, renamed = 0, skipped = 0, bytes = 0;
    try {
      const existing = (await Store.list()).map((s) => s.name);
      for (const f of files) {
        if (f.size > MAX_BYTES * 2) { skipped++; continue; }
        let items;
        try { items = await readImport(f); } catch (e) { toast(e.message || `Could not read ${f.name}.`, true); skipped++; continue; }
        for (const it of items) {
          if (added >= MAX_IMPORT) { skipped++; continue; }
          const ok = typeof it.name === "string" && typeof it.xml === "string" && typeof it.tag === "string"
            && !nameError(it.name) && !tagError(it.tag) && it.xml.trim() && Store.byteLength(it.xml) <= MAX_BYTES;
          if (!ok || (bytes += it.xml.length) > 50 * 1024 * 1024) { skipped++; continue; }
          const name = uniqueName(it.name, existing);
          if (name !== it.name) renamed++;
          await createLocal(name, it.tag, it.xml);
          existing.push(name);
          added++;
        }
      }
    } catch (e) {
      toast(e.message, true);
      return;
    }
    const parts = [`Imported ${added}`];
    if (renamed) parts.push(`${renamed} renamed`);
    if (skipped) parts.push(`${skipped} skipped`);
    toast(parts.join(", "), !added && !!skipped);
    await refreshLists();
    if (sw().open) renderList();
  }

  q("#sw-import").addEventListener("click", () => q("#sw-file").click());
  q("#sw-file").addEventListener("change", async (e) => {
    const files = [...e.target.files];
    e.target.value = "";
    if (files.length) await importFiles(files);
  });
  q("#sw-export").addEventListener("click", async () => {
    try {
      const all = await Store.all();
      if (!all.length) { toast("You have no samples to export.", true); return; }
      download(`soapviz-samples-${new Date().toISOString().slice(0, 10)}.json`, exportJSON(all));
    } catch (e) { toast(e.message, true); }
  });
  q("#sw-clear").addEventListener("click", async () => {
    try {
      const n = (await Store.list()).length;
      if (!n) { toast("You have no samples to delete."); return; }
      if (!(await askConfirm("Delete all your samples?", `This removes all ${n} sample${n === 1 ? "" : "s"} saved in this browser. Team samples are not affected. Export first if you might need them.`, "Delete all", true))) return;
      await Store.clear();
      if (S.active && S.active.where === "local") setActive(null);
      S.previous = S.previous && S.previous.where === "shared" ? S.previous : null;
      toast("Deleted all your samples");
      await refreshLists();
      renderList();
    } catch (e) { toast(e.message, true); }
  });

  // ---- wiring ----
  q("#samples-btn").addEventListener("click", openSwitcher);
  q("#save-btn").addEventListener("click", () => { save(); });
  q("#saveas-btn").addEventListener("click", () => {
    saveAsFlow(S.active ? { title: "Save as", name: S.active.name, tag: S.active.tag } : {});
  });

  document.addEventListener("keydown", (e) => {
    if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
    const k = e.key.toLowerCase();
    if (k === "k") {
      e.preventDefault();
      if (sw().open) closeSwitcher();
      else if (!document.querySelector("dialog[open]")) openSwitcher();
    } else if (k === "s") {
      e.preventDefault();
      if (!document.querySelector("dialog[open]")) save();
    }
  });

  xmlTA.addEventListener("input", renderActive);
  document.addEventListener("soapviz:xml", renderActive);
  window.addEventListener("beforeunload", (e) => {
    if (S.active && isDirty()) { e.preventDefault(); e.returnValue = ""; }
  });

  async function restoreLast() {
    let id = null;
    try { id = localStorage.getItem(LAST_KEY); } catch { /* ignore */ }
    if (!id || !Store.available || xmlTA.value !== "") return;
    try {
      const rec = await Store.get(id);
      if (!rec) { localStorage.removeItem(LAST_KEY); return; }
      setActive({ where: "local", key: rec.id, name: rec.name, tag: rec.tag || "", baseline: rec.xml, updatedAt: rec.updatedAt });
      setXML(rec.xml);
      run(null);
    } catch { /* storage unavailable: start empty */ }
  }

  // The app calls this once its config is known. cfg is the server's /api/config, or {} for the static build.
  window.soapvizSamples = {
    init(cfg) {
      cfg = cfg || {};
      Tags.setOverrides(cfg.tags);
      S.shared = { enabled: !!(cfg.shared && cfg.shared.enabled), writable: !!(cfg.shared && cfg.shared.writable) };
      renderActive();
      restoreLast();
    },
    guardReplace,
    detach() { setActive(null); },
  };
})();
