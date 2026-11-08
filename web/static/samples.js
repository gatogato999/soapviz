"use strict";
// Samples UI: a toggle menu to open, save and organize named XML samples.
// "My samples" live in this browser (samples-store.js); "Team samples" live in a
// folder on the server and exist only when the server enables them. Both can be
// grouped in one level of folders.
// Relies on app.js globals at call time (not at load): setXML, run, closed.
(() => {
  const Tags = window.soapvizTags;
  const Store = window.soapvizStore;
  const q = (s) => document.querySelector(s);
  const xmlTA = q("#xml"); // samples.js loads before app.js, so it can't use app.js's xmlEl at load time
  const menu = q("#sw-menu"), menuBtn = q("#samples-btn");

  const LAST_KEY = "soapviz.last";          // id of the last open "my sample"
  const COLLAPSED_KEY = "soapviz.collapsed"; // folders the user collapsed
  const MAX_NAME = 80;
  const MAX_BYTES = 8 * 1024 * 1024; // matches the server's default request limit
  const MAX_IMPORT = 500;
  const SEP = "\u001f"; // joins folder and name into one key for team samples

  const emptyData = () => ({ samples: [], folders: [] });
  const S = {
    shared: { enabled: false, writable: false },
    active: null,   // {where: "local"|"shared", key, folder, name, tag, baseline, updatedAt}
    previous: null, // {where, key}: what Ctrl+K then Enter returns to
    local: emptyData(), team: emptyData(), teamError: "", teamLoaded: false,
    collapsed: new Set(),
    rows: [], hl: -1,
  };
  try { S.collapsed = new Set(JSON.parse(localStorage.getItem(COLLAPSED_KEY) || "[]")); } catch { /* start expanded */ }

  const tkey = (folder, name) => folder + SEP + name;
  const parseTkey = (k) => { const i = k.indexOf(SEP); return { folder: k.slice(0, i), name: k.slice(i + 1) }; };
  const sameFolder = (a, b) => (a || "").toLowerCase() === (b || "").toLowerCase();

  // ---- validation (mirrors shared.ValidateName / ValidateTag in Go; the server is authoritative) ----
  const BAD_NAME = /[\x00-\x1f\x7f/\\:*?"<>|[\]]/;
  function nameError(n, what = "name") {
    if (n === "" || n !== n.trim()) return `Enter a ${what} (no spaces at the start or end).`;
    if ([...n].length > MAX_NAME) return `A ${what} is at most ${MAX_NAME} characters.`;
    if (n[0] === ".") return `A ${what} can't start with a dot.`;
    if (BAD_NAME.test(n)) return `A ${what} can't contain / \\ : * ? " < > | [ ] or control characters.`;
    return "";
  }
  const folderError = (f) => (f === "" ? "" : nameError(f, "folder name"));
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

  const el = (tag, cls, text) => {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  };

  // ---- team samples API ----
  const qs = (o) => {
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(o || {})) if (v) p.set(k, v);
    const s = p.toString();
    return s ? "?" + s : "";
  };

  async function teamFetch(method, path, params, body) {
    const res = await fetch("api/shared" + path + qs(params), {
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

  // Resolves to {name, tag, folder, where} or null.
  // opts: title, name, tag, folder, where, lockWhere, note, error
  function askSave(opts) {
    const d = q("#dlg-save"), nameEl = q("#save-name"), tagEl = q("#save-tag"), folderEl = q("#save-folder");
    q("#dlg-save-title").textContent = opts.title;
    nameEl.value = opts.name || "";
    tagEl.value = opts.tag || "";
    folderEl.value = opts.folder || "";
    q("#save-error").textContent = opts.error || "";
    q("#save-note").textContent = opts.note || "";
    const canTeam = S.shared.enabled && S.shared.writable && !opts.lockWhere;
    const whereEl = q("#save-where");
    whereEl.hidden = !canTeam;
    q("#save-where-team").disabled = !canTeam;
    const initial = canTeam ? opts.where || "local" : opts.lockWhere || "local";
    for (const r of whereEl.querySelectorAll("input[name=where]")) r.checked = r.value === initial;
    const currentWhere = () => {
      const c = whereEl.querySelector("input[name=where]:checked");
      return canTeam && c ? c.value : initial;
    };
    const fillFolders = () => {
      const data = currentWhere() === "shared" ? S.team : S.local;
      q("#folder-list").replaceChildren(...data.folders.map((f) => Object.assign(document.createElement("option"), { value: f })));
    };
    fillFolders();
    whereEl.onchange = fillFolders;
    const paintChip = () => {
      const slot = q("#save-chip");
      slot.replaceChildren();
      if (tagEl.value && Tags.valid(tagEl.value)) slot.append(Tags.chip(tagEl.value));
    };
    paintChip();
    tagEl.oninput = paintChip;
    return whenClosed(d, "cancel").then((v) => (v !== "ok" ? null
      : { name: nameEl.value, tag: tagEl.value, folder: folderEl.value, where: currentWhere() }));
  }

  q("#save-form").addEventListener("submit", (e) => {
    if (e.submitter && e.submitter.value === "cancel") return;
    const err = nameError(q("#save-name").value) || tagError(q("#save-tag").value) || folderError(q("#save-folder").value);
    if (err) {
      e.preventDefault();
      q("#save-error").textContent = err;
    }
  });

  // Resolves to a folder name or null.
  function askFolder({ title, name, error }) {
    q("#dlg-folder-title").textContent = title;
    q("#folder-name").value = name || "";
    q("#folder-error").textContent = error || "";
    return whenClosed(q("#dlg-folder"), "cancel").then((v) => (v === "ok" ? q("#folder-name").value : null));
  }

  q("#folder-form").addEventListener("submit", (e) => {
    if (e.submitter && e.submitter.value === "cancel") return;
    const err = folderError(q("#folder-name").value) || (q("#folder-name").value === "" ? "Enter a folder name." : "");
    if (err) {
      e.preventDefault();
      q("#folder-error").textContent = err;
    }
  });

  // ---- active sample and dirty state ----
  function isDirty() {
    if (S.active) return xmlTA.value !== S.active.baseline;
    return xmlTA.value.trim() !== "";
  }

  function renderActive() {
    const el0 = q("#active-sample"), a = S.active;
    el0.replaceChildren();
    q(".btn-label").hidden = !!a;
    if (a) {
      const c = Tags.chip(a.tag);
      if (c) el0.append(c);
      if (a.folder) el0.append(el("span", "afolder", a.folder + " /"));
      el0.append(el("span", "aname", a.name));
      if (a.where === "shared") el0.append(el("span", "awhere", "team"));
      if (isDirty()) el0.append(Object.assign(el("span", "adirty", "●"), { title: "Unsaved changes" }));
      const full = (a.folder ? a.folder + " / " : "") + a.name;
      menuBtn.setAttribute("aria-label", `Samples menu. Open: ${full}${isDirty() ? ", unsaved changes" : ""}`);
      menuBtn.title = `${full} (Ctrl+K to switch)`;
    } else {
      menuBtn.setAttribute("aria-label", "Samples menu");
      menuBtn.title = "Open or manage saved samples (Ctrl+K)";
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

  const activeFrom = (where, rec, extra) => ({
    where, key: where === "local" ? rec.id : tkey(rec.folder || "", rec.name), folder: rec.folder || "", name: rec.name,
    tag: rec.tag || "", baseline: rec.xml, updatedAt: rec.updatedAt, ...extra,
  });

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
      if (where === "local") rec = await Store.get(key);
      else { const t = parseTkey(key); rec = await teamFetch("GET", "/sample", t); }
    } catch (e) {
      toast(e.status === 404 ? "That sample no longer exists." : e.message, true);
      return false;
    }
    if (!rec) { toast("That sample no longer exists.", true); return false; }
    setActive(activeFrom(where, rec));
    closed.clear();
    setXML(rec.xml);
    run(null);
    if (where === "local") Store.touch(rec.id).catch(() => {});
    return true;
  }

  async function createLocal(r, xml) {
    const m = await Store.create({ name: r.name, tag: r.tag, folder: r.folder, xml });
    Store.persist();
    return m;
  }

  async function persistNew(r, xml) {
    if (r.where === "shared") {
      try {
        const info = await teamFetch("PUT", "/sample", { folder: r.folder, name: r.name }, { tag: r.tag, xml });
        setActive(activeFrom("shared", { ...info, xml }));
      } catch (e) {
        if (e.code === "exists") throw Object.assign(new Error("A team sample with that name already exists in that folder."), { code: "exists" });
        throw e;
      }
    } else {
      const m = await createLocal(r, xml);
      setActive(activeFrom("local", { ...m, xml }));
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
        if (!m) { toast("That sample was deleted elsewhere. Save it as a new one.", true); setActive(null); return saveAsFlow({ name: a.name, tag: a.tag, folder: a.folder }); }
        a.baseline = xml; a.updatedAt = m.updatedAt;
      } else if (S.shared.writable) {
        const put = (ifUpdatedAt) => teamFetch("PUT", "/sample", { folder: a.folder, name: a.name }, { tag: a.tag, xml, ifUpdatedAt });
        let info;
        try {
          info = await put(a.updatedAt);
        } catch (e) {
          if (e.code !== "conflict") throw e;
          const ok = await askConfirm("Someone else changed this sample",
            `"${a.name}" was changed on the server after you opened it. Overwrite their version with yours?`, "Overwrite", true);
          if (!ok) return false;
          info = await put(e.updatedAt);
        }
        a.baseline = xml; a.updatedAt = info.updatedAt;
      } else {
        return saveAsFlow({ title: "Save a copy", name: a.name, tag: a.tag, folder: a.folder, lockWhere: "local",
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

  // ---- the menu (a toggle popover anchored to the Samples button) ----
  const isOpen = () => menu.matches(":popover-open");
  const modalOpen = () => !!document.querySelector("dialog[open]");

  function positionMenu() {
    const r = menuBtn.getBoundingClientRect();
    const w = menu.offsetWidth || 460;
    menu.style.top = `${Math.round(r.bottom + 6)}px`;
    menu.style.left = `${Math.round(Math.max(8, Math.min(r.left, window.innerWidth - w - 8)))}px`;
  }

  async function refreshLists() {
    const jobs = [];
    if (Store.available) {
      jobs.push(Store.listAll().then((d) => { S.local = d; }).catch((e) => { S.local = emptyData(); toast(e.message, true); }));
    }
    if (S.shared.enabled) {
      jobs.push(teamFetch("GET", "").then((d) => { S.team = { samples: d.samples, folders: d.folders }; S.teamError = ""; })
        .catch((e) => { S.team = emptyData(); S.teamError = e.message; })
        .finally(() => { S.teamLoaded = true; }));
    }
    await Promise.all(jobs);
  }

  async function openMenu() {
    if (isOpen() || modalOpen()) return;
    menu.showPopover();
    menuBtn.setAttribute("aria-expanded", "true");
    positionMenu();
    q("#sw-filter").value = "";
    renderList(); // immediately, with whatever is cached; then refresh
    q("#sw-filter").focus();
    await refreshLists();
    if (isOpen()) renderList({ keepHighlight: true });
  }

  function closeMenu(returnFocus) {
    if (!isOpen()) return;
    menu.hidePopover();
    menuBtn.setAttribute("aria-expanded", "false");
    if (returnFocus) menuBtn.focus();
  }

  menuBtn.addEventListener("click", () => { if (isOpen()) closeMenu(); else openMenu(); });
  document.addEventListener("pointerdown", (e) => { // click outside closes it
    if (isOpen() && !modalOpen() && !menu.contains(e.target) && !menuBtn.contains(e.target)) closeMenu();
  });
  window.addEventListener("resize", () => { if (isOpen()) positionMenu(); });

  // ---- filter: words match names (and folder names); #tag matches a tag exactly ----
  function parseFilter() {
    const words = [], tags = new Set();
    for (const tok of q("#sw-filter").value.toLowerCase().split(/\s+/).filter(Boolean)) {
      if (tok[0] === "#") { if (tok.length > 1) tags.add(tok.slice(1)); } else words.push(tok);
    }
    return { words, tags, active: words.length > 0 || tags.size > 0 };
  }

  function matches(item, f) {
    if (f.tags.size && !f.tags.has((item.tag || "").toLowerCase())) return false;
    const hay = `${item.folder || ""} ${item.name}`.toLowerCase();
    return f.words.every((w) => hay.includes(w));
  }

  // Clicking a tag chip puts "#tag" in the filter box (or removes it), so the box is the one source of truth.
  function toggleTagToken(tag) {
    const input = q("#sw-filter");
    const want = "#" + tag.toLowerCase();
    const toks = input.value.split(/\s+/).filter(Boolean);
    const i = toks.findIndex((t) => t.toLowerCase() === want);
    if (i >= 0) toks.splice(i, 1); else toks.push("#" + tag);
    input.value = toks.length ? toks.join(" ") + " " : "";
    input.focus();
    renderList();
  }

  function renderTagBar(f) {
    const bar = q("#sw-tags");
    const counts = new Map();
    const all = [...S.local.samples, ...(S.shared.enabled ? S.team.samples : [])];
    for (const s of all) {
      if (!s.tag) continue;
      const k = s.tag.toLowerCase();
      const cur = counts.get(k) || { tag: s.tag, n: 0 };
      cur.n++;
      counts.set(k, cur);
    }
    bar.replaceChildren();
    bar.hidden = counts.size === 0;
    for (const { tag, n } of [...counts.values()].sort((a, b) => b.n - a.n || a.tag.localeCompare(b.tag))) {
      const btn = el("button", "chip chipbtn");
      btn.type = "button";
      btn.dataset.c = Tags.color(tag);
      btn.dataset.tag = tag;
      btn.setAttribute("aria-pressed", String(f.tags.has(tag.toLowerCase())));
      btn.title = `Show only ${tag} (${n})`;
      btn.textContent = `${tag} ${n}`;
      bar.append(btn);
    }
  }

  // ---- rendering the list ----
  function actionButton(label, action, where, key, kind) {
    const b = el("button", "", label);
    b.type = "button";
    b.dataset.a = action;
    b.dataset.where = where;
    b.dataset.key = key;
    b.dataset.kind = kind;
    if (/delete/.test(action)) b.classList.add("danger-hover");
    return b;
  }

  function renderList({ keepHighlight } = {}) {
    const list = q("#sw-list");
    const f = parseFilter();
    const prev = keepHighlight && S.rows[S.hl] ? { kind: S.rows[S.hl].kind, where: S.rows[S.hl].where, key: S.rows[S.hl].key } : null;
    list.replaceChildren();
    S.rows = [];
    renderTagBar(f);

    const group = (title, total, where, canNewFolder) => {
      const li = el("li", "sw-group");
      li.append(el("span", "", `${title} (${total})`));
      if (canNewFolder) li.append(actionButton("New folder", "newfolder", where, "", "group"));
      list.append(li);
    };
    const note = (text, cls) => list.append(el("li", "sw-note" + (cls ? " " + cls : ""), text));

    const addRow = (kind, where, key, build, actions, indent) => {
      const li = el("li", "srow" + (kind === "folder" ? " frow" : "") + (indent ? " indent" : ""));
      li.setAttribute("role", "presentation");
      const opt = el("div", "sopen");
      opt.setAttribute("role", "option");
      opt.id = "opt-" + S.rows.length;
      opt.dataset.kind = kind;
      opt.dataset.where = where;
      opt.dataset.key = key;
      build(opt);
      const acts = el("span", "sacts");
      for (const a of actions) acts.append(actionButton(a[0], a[1], where, key, kind));
      li.append(opt, acts);
      list.append(li);
      S.rows.push({ kind, where, key, opt });
    };

    const addSample = (where, s, indent) => {
      const key = where === "local" ? s.id : tkey(s.folder || "", s.name);
      const actions = where === "local"
        ? [["Edit", "rename"], ["Export", "export"], ...(S.shared.writable ? [["Publish", "publish"]] : []), ["Delete", "delete"]]
        : [...(S.shared.writable ? [["Edit", "rename"]] : []), ["Copy to mine", "copy"], ...(S.shared.writable ? [["Delete", "delete"]] : [])];
      addRow("sample", where, key, (opt) => {
        opt.append(Tags.chip(s.tag) || el("span", "chip-gap"));
        opt.append(el("span", "sname", s.name));
        if (S.active && S.active.where === where && S.active.key === key) opt.append(el("span", "sopenmark", "open"));
        opt.append(el("span", "smeta", `${formatBytes(s.bytes)} · ${ago(s.updatedAt)}`));
      }, actions, indent);
    };

    const addGroup = (where, title, data, canEditFolders, emptyHint) => {
      const mine = data.samples;
      group(title, mine.length, where, canEditFolders);
      const total = mine.length + data.folders.length;
      let shown = 0;
      const order = where === "local" ? (a, b) => b.lastUsedAt - a.lastUsedAt : () => 0; // team: server sorts by name
      for (const folder of data.folders) {
        const inside = mine.filter((s) => sameFolder(s.folder, folder));
        const visible = inside.filter((s) => matches(s, f)).sort(order);
        if (f.active && !visible.length) continue;
        shown++;
        const cKey = `${where}:${folder.toLowerCase()}`;
        const collapsed = !f.active && S.collapsed.has(cKey);
        const folderActions = canEditFolders ? [["Rename", "frename"], ["Delete", "fdelete"]] : [];
        addRow("folder", where, folder, (opt) => {
          opt.append(el("span", "fcaret", collapsed ? "▸" : "▾"));
          opt.append(el("span", "sname", folder));
          opt.append(el("span", "fcount", f.active ? `${visible.length} of ${inside.length}` : String(inside.length)));
          opt.setAttribute("aria-expanded", String(!collapsed));
        }, folderActions);
        if (!collapsed) {
          if (!inside.length && !f.active) note("Empty folder", "indent");
          for (const s of visible) { addSample(where, s, true); shown++; }
        }
      }
      for (const s of mine.filter((x) => !x.folder).filter((x) => matches(x, f)).sort(order)) { addSample(where, s, false); shown++; }
      if (!total) note(emptyHint);
      else if (f.active && !shown) note("No match.");
    };

    // My samples
    if (!Store.available) {
      group("My samples", 0, "local", false);
      note("Saving in this browser is not available here (private mode or blocked storage).", "warn");
    } else {
      addGroup("local", "My samples", S.local, true, "Nothing saved yet. Paste some XML and press Ctrl+S.");
    }

    // Team samples
    if (S.shared.enabled) {
      if (S.teamError) {
        group("Team samples", 0, "shared", false);
        note(`Could not load team samples: ${S.teamError}`, "warn");
      } else if (!S.teamLoaded) {
        group("Team samples", 0, "shared", false);
        note("Loading…");
      } else {
        addGroup("shared", "Team samples", S.team, S.shared.writable,
          S.shared.writable ? "No team samples yet. Use Publish on one of yours." : "No team samples yet. An admin adds .xml files to the server's shared folder.");
      }
    }

    // initial highlight: where you came from, else the first sample that isn't open
    const isSample = (r) => r.kind === "sample";
    let hl = -1;
    if (prev) hl = S.rows.findIndex((r) => r.kind === prev.kind && r.where === prev.where && r.key === prev.key);
    if (hl < 0 && S.previous) hl = S.rows.findIndex((r) => isSample(r) && r.where === S.previous.where && r.key === S.previous.key);
    if (hl < 0) hl = S.rows.findIndex((r) => isSample(r) && !(S.active && r.where === S.active.where && r.key === S.active.key));
    if (hl < 0) hl = S.rows.findIndex(isSample);
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

  function toggleFolder(where, folder) {
    const k = `${where}:${folder.toLowerCase()}`;
    if (S.collapsed.has(k)) S.collapsed.delete(k); else S.collapsed.add(k);
    try { localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...S.collapsed])); } catch { /* ignore */ }
    renderList({ keepHighlight: true });
  }

  function activateRow(r) {
    if (r.kind === "folder") toggleFolder(r.where, r.key);
    else { closeMenu(); openSample(r.where, r.key); }
  }

  q("#sw-filter").addEventListener("input", () => renderList());
  q("#sw-filter").addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!S.rows.length) return;
      const step = e.key === "ArrowDown" ? 1 : -1;
      setHighlight((S.hl + step + S.rows.length) % S.rows.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const r = S.rows[S.hl];
      if (r) activateRow(r);
    }
  });

  q("#sw-tags").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-tag]");
    if (b) toggleTagToken(b.dataset.tag);
  });

  q("#sw-list").addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-a]");
    if (btn) { await rowAction(btn.dataset.a, btn.dataset.where, btn.dataset.key); return; }
    const opt = e.target.closest(".sopen");
    if (!opt) return;
    const r = S.rows.find((x) => x.opt === opt);
    if (r) { setHighlight(S.rows.indexOf(r)); activateRow(r); }
  });

  // ---- row and folder actions ----
  const dataFor = (where) => (where === "local" ? S.local : S.team);

  async function reload() {
    await refreshLists();
    if (isOpen()) renderList({ keepHighlight: true });
  }

  async function rowAction(action, where, key) {
    try {
      if (action === "newfolder") await newFolder(where);
      else if (action === "frename" || action === "fdelete") await folderAction(action, where, key);
      else if (where === "local") await localSampleAction(action, key);
      else await teamSampleAction(action, key);
    } catch (e) {
      toast(e.message, true);
    }
    await reload();
  }

  async function localSampleAction(action, id) {
    const rec = await Store.get(id);
    if (!rec) { toast("That sample no longer exists.", true); return; }
    if (action === "rename") {
      let o = { title: "Edit sample", name: rec.name, tag: rec.tag, folder: rec.folder, lockWhere: "local" };
      for (;;) {
        const r = await askSave(o);
        if (!r) return;
        try {
          const m = await Store.update(id, { name: r.name, tag: r.tag, folder: r.folder });
          if (S.active && S.active.where === "local" && S.active.key === id) {
            Object.assign(S.active, { name: m.name, tag: m.tag, folder: m.folder });
            renderActive();
          }
          return;
        } catch (e) {
          if (e.code === "exists") { o = { ...o, ...r, error: e.message }; continue; }
          throw e;
        }
      }
    } else if (action === "export") {
      download(`${rec.name}.json`, exportJSON([rec], rec.folder ? [rec.folder] : []));
    } else if (action === "publish") {
      await publish(rec);
    } else if (action === "delete") {
      if (!(await askConfirm("Delete sample?", `"${rec.name}" will be removed from this browser. This can't be undone.`, "Delete", true))) return;
      await Store.remove(id);
      if (S.active && S.active.where === "local" && S.active.key === id) setActive(null);
      if (S.previous && S.previous.where === "local" && S.previous.key === id) S.previous = null;
    }
  }

  async function teamSampleAction(action, key) {
    const t = parseTkey(key);
    if (action === "copy") {
      const rec = await teamFetch("GET", "/sample", t);
      const taken = S.local.samples.filter((s) => sameFolder(s.folder, rec.folder || "")).map((s) => s.name);
      await createLocal({ name: uniqueName(rec.name, taken), tag: rec.tag || "", folder: rec.folder || "" }, rec.xml);
      toast(`Copied "${rec.name}" to your samples`);
    } else if (action === "rename") {
      const entry = S.team.samples.find((s) => sameFolder(s.folder, t.folder) && s.name === t.name);
      if (!entry) { toast("That sample no longer exists.", true); return; }
      let o = { title: "Edit team sample", name: entry.name, tag: entry.tag, folder: entry.folder, lockWhere: "shared" };
      for (;;) {
        const r = await askSave(o);
        if (!r) return;
        try {
          const info = await teamFetch("PATCH", "/sample", t, { folder: r.folder, name: r.name, tag: r.tag, ifUpdatedAt: entry.updatedAt });
          if (S.active && S.active.where === "shared" && S.active.key === key) {
            Object.assign(S.active, { key: tkey(info.folder || "", info.name), folder: info.folder || "", name: info.name, tag: info.tag || "", updatedAt: info.updatedAt });
            renderActive();
          }
          return;
        } catch (e) {
          if (e.code === "exists") { o = { ...o, ...r, error: e.message }; continue; }
          if (e.code === "conflict") { toast("Someone else changed that sample. The list was refreshed; try again.", true); return; }
          throw e;
        }
      }
    } else if (action === "delete") {
      const label = (t.folder ? t.folder + " / " : "") + t.name;
      if (!(await askConfirm("Delete team sample?", `"${label}" will be removed for everyone using this server. This can't be undone.`, "Delete", true))) return;
      await teamFetch("DELETE", "/sample", t);
      if (S.active && S.active.where === "shared" && S.active.key === key) setActive(null);
      if (S.previous && S.previous.where === "shared" && S.previous.key === key) S.previous = null;
    }
  }

  async function publish(rec) {
    const params = { folder: rec.folder, name: rec.name };
    try {
      await teamFetch("PUT", "/sample", params, { tag: rec.tag, xml: rec.xml });
    } catch (e) {
      if (e.code !== "exists") throw e;
      const list = await teamFetch("GET", "");
      const existing = list.samples.find((s) => sameFolder(s.folder, rec.folder) && s.name.toLowerCase() === rec.name.toLowerCase());
      const label = (rec.folder ? rec.folder + " / " : "") + rec.name;
      const ok = await askConfirm("Replace the team sample?", `The team already has "${label}". Replace it with your version?`, "Replace", true);
      if (!ok || !existing) return;
      await teamFetch("PUT", "/sample", { folder: existing.folder, name: existing.name }, { tag: rec.tag, xml: rec.xml, ifUpdatedAt: existing.updatedAt });
    }
    toast(`Published "${rec.name}" to the team`);
  }

  async function newFolder(where) {
    let o = { title: where === "shared" ? "New team folder" : "New folder", name: "" };
    for (;;) {
      const name = await askFolder(o);
      if (name === null) return;
      try {
        if (where === "local") await Store.createFolder(name);
        else await teamFetch("PUT", "/folder", { name });
        toast(`Created folder "${name}"`);
        return;
      } catch (e) {
        if (e.code === "exists") { o = { ...o, name, error: "A folder with that name already exists." }; continue; }
        throw e;
      }
    }
  }

  async function folderAction(action, where, folder) {
    const data = dataFor(where);
    const inside = data.samples.filter((s) => sameFolder(s.folder, folder));
    if (action === "frename") {
      let o = { title: "Rename folder", name: folder };
      for (;;) {
        const name = await askFolder(o);
        if (name === null || name === folder) return;
        try {
          if (where === "local") await Store.renameFolder(folder, name);
          else await teamFetch("PATCH", "/folder", { name: folder }, { name });
          if (S.collapsed.delete(`${where}:${folder.toLowerCase()}`)) S.collapsed.add(`${where}:${name.toLowerCase()}`);
          if (S.active && S.active.where === where && sameFolder(S.active.folder, folder)) {
            S.active.folder = name;
            if (where === "shared") S.active.key = tkey(name, S.active.name);
            renderActive();
          }
          return;
        } catch (e) {
          if (e.code === "exists") { o = { ...o, name, error: "A folder with that name already exists." }; continue; }
          throw e;
        }
      }
    }
    // delete
    if (where === "local") {
      const n = inside.length;
      const body = n
        ? `"${folder}" will be removed. Its ${n} sample${n === 1 ? "" : "s"} will not be deleted; ${n === 1 ? "it moves" : "they move"} to the top level.`
        : `"${folder}" is empty and will be removed.`;
      if (!(await askConfirm("Delete folder?", body, "Delete folder", true))) return;
      await Store.deleteFolder(folder);
      if (S.active && S.active.where === "local" && sameFolder(S.active.folder, folder)) {
        const rec = await Store.get(S.active.key); // it may have been renamed to avoid a clash
        if (rec) Object.assign(S.active, { name: rec.name, folder: "" });
        renderActive();
      }
    } else {
      if (inside.length) { toast("A team folder must be empty before it can be deleted. Move or delete its samples first.", true); return; }
      if (!(await askConfirm("Delete team folder?", `"${folder}" is empty and will be removed for everyone.`, "Delete folder", true))) return;
      await teamFetch("DELETE", "/folder", { name: folder });
    }
  }

  // ---- export / import ----
  function exportJSON(recs, folders) {
    return JSON.stringify({
      format: "soapviz-samples", version: 1, exportedAt: new Date().toISOString(), folders: folders || [],
      samples: recs.map((r) => ({ folder: r.folder || "", name: r.name, tag: r.tag || "", xml: r.xml, updatedAt: r.updatedAt })),
    }, null, 2);
  }

  const FILE_TAG_RE = /^(.*\S)\s*\[([A-Za-z]{1,4})\]$/;

  // Turns an imported file into candidates. Anything malformed is dropped by the caller.
  async function readImport(file) {
    const text = await file.text();
    if (/\.json$/i.test(file.name)) {
      const data = JSON.parse(text);
      if (!data || data.format !== "soapviz-samples" || !Array.isArray(data.samples)) throw new Error(`${file.name} is not a soapviz export.`);
      return {
        folders: Array.isArray(data.folders) ? data.folders : [],
        items: data.samples.map((s) => ({ name: s && s.name, tag: (s && s.tag) || "", folder: (s && s.folder) || "", xml: s && s.xml })),
      };
    }
    let stem = file.name.replace(/\.[^.]*$/, "");
    let tag = "";
    const m = FILE_TAG_RE.exec(stem);
    if (m) { stem = m[1]; tag = m[2]; }
    return { folders: [], items: [{ name: stem.trim(), tag, folder: "", xml: text }] };
  }

  async function importFiles(files) {
    let added = 0, renamed = 0, skipped = 0, bytes = 0;
    try {
      const cur = await Store.listAll();
      const byFolder = new Map(); // lowercase folder -> names taken
      for (const s of cur.samples) {
        const k = (s.folder || "").toLowerCase();
        if (!byFolder.has(k)) byFolder.set(k, []);
        byFolder.get(k).push(s.name);
      }
      for (const f of files) {
        if (f.size > MAX_BYTES * 2) { skipped++; continue; }
        let data;
        try { data = await readImport(f); } catch (e) { toast(e.message || `Could not read ${f.name}.`, true); skipped++; continue; }
        for (const name of data.folders.slice(0, 200)) {
          if (typeof name === "string" && !folderError(name) && name) { try { await Store.createFolder(name); } catch { /* exists */ } }
        }
        for (const it of data.items) {
          if (added >= MAX_IMPORT) { skipped++; continue; }
          const ok = typeof it.name === "string" && typeof it.xml === "string" && typeof it.tag === "string" && typeof it.folder === "string"
            && !nameError(it.name) && !tagError(it.tag) && !folderError(it.folder) && it.xml.trim() && Store.byteLength(it.xml) <= MAX_BYTES;
          if (!ok || (bytes += it.xml.length) > 50 * 1024 * 1024) { skipped++; continue; }
          const k = it.folder.toLowerCase();
          if (!byFolder.has(k)) byFolder.set(k, []);
          const taken = byFolder.get(k);
          const name = uniqueName(it.name, taken);
          if (name !== it.name) renamed++;
          await createLocal({ name, tag: it.tag, folder: it.folder }, it.xml);
          taken.push(name);
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
    await reload();
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
      if (!all.samples.length && !all.folders.length) { toast("You have no samples to export.", true); return; }
      download(`soapviz-samples-${new Date().toISOString().slice(0, 10)}.json`, exportJSON(all.samples, all.folders));
    } catch (e) { toast(e.message, true); }
  });
  q("#sw-clear").addEventListener("click", async () => {
    try {
      const n = (await Store.list()).length;
      if (!n) { toast("You have no samples to delete."); return; }
      if (!(await askConfirm("Delete all your samples?", `This removes all ${n} sample${n === 1 ? "" : "s"} (and your folders) saved in this browser. Team samples are not affected. Export first if you might need them.`, "Delete all", true))) return;
      await Store.clear();
      if (S.active && S.active.where === "local") setActive(null);
      S.previous = S.previous && S.previous.where === "shared" ? S.previous : null;
      toast("Deleted all your samples");
      await reload();
    } catch (e) { toast(e.message, true); }
  });

  // ---- wiring ----
  q("#save-btn").addEventListener("click", () => { save(); });
  q("#saveas-btn").addEventListener("click", () => {
    saveAsFlow(S.active ? { title: "Save as", name: S.active.name, tag: S.active.tag, folder: S.active.folder } : {});
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && isOpen() && !modalOpen()) { e.preventDefault(); closeMenu(true); return; }
    if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
    const k = e.key.toLowerCase();
    if (k === "k") {
      e.preventDefault();
      if (isOpen()) closeMenu(true); else if (!modalOpen()) openMenu();
    } else if (k === "s") {
      e.preventDefault();
      if (!modalOpen()) save();
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
      setActive(activeFrom("local", rec));
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
