"use strict";
// "My samples": named XML samples kept in this browser's IndexedDB, optionally
// grouped in virtual folders (one level). Nothing here talks to the server.
// IndexedDB (not localStorage) because samples can be megabytes and localStorage
// is capped near 5 MB and blocks the page.
//
// Schema v2: store "samples" (id, name, tag, folder, xml, ...) with a unique
// index on (folderKey, nameKey), so a name is unique within its folder, ignoring
// case; store "folders" (key, name) holds every folder, including empty ones.
// v1 had one flat list with a unique index on nameKey; upgrading adds the folder
// fields to existing records and keeps everything.
(() => {
  const DB = "soapviz", STORE = "samples", FOLDERS = "folders", VERSION = 2;
  const available = typeof indexedDB !== "undefined";
  let dbp = null;

  function fail(message, code) {
    return Object.assign(new Error(message), { code });
  }

  function mapErr(err, existsMsg) {
    const n = err && err.name;
    if (n === "ConstraintError") return fail(existsMsg || "A sample with that name already exists in that folder.", "exists");
    if (n === "QuotaExceededError") return fail("Browser storage is full. Export and delete some samples.", "quota");
    return err instanceof Error ? err : fail("Browser storage failed.", "storage");
  }

  const unavailable = () => fail("Saving in this browser is not available (private mode or blocked storage).", "unavailable");

  function open() {
    if (!available) return Promise.reject(unavailable());
    if (!dbp) {
      dbp = new Promise((resolve, reject) => {
        let r;
        try { r = indexedDB.open(DB, VERSION); } catch (e) { dbp = null; reject(unavailable()); return; }
        r.onupgradeneeded = (e) => {
          const db = r.result, old = e.oldVersion;
          const samples = old < 1 ? db.createObjectStore(STORE, { keyPath: "id" }) : r.transaction.objectStore(STORE);
          if (samples.indexNames.contains("nameKey")) samples.deleteIndex("nameKey"); // v1: names unique across everything
          if (!samples.indexNames.contains("folderName")) samples.createIndex("folderName", ["folderKey", "nameKey"], { unique: true });
          if (!db.objectStoreNames.contains(FOLDERS)) db.createObjectStore(FOLDERS, { keyPath: "key" });
          if (old >= 1) { // v1 records have no folder: put them at the top level
            samples.openCursor().onsuccess = (ev) => {
              const c = ev.target.result;
              if (!c) return;
              if (c.value.folderKey === undefined) c.update({ ...c.value, folder: "", folderKey: "" });
              c.continue();
            };
          }
        };
        r.onsuccess = () => {
          r.result.onversionchange = () => { r.result.close(); dbp = null; }; // let a newer tab upgrade
          resolve(r.result);
        };
        r.onblocked = () => { dbp = null; reject(fail("Close your other soapviz tabs and reload to finish updating saved samples.", "blocked")); };
        r.onerror = () => { dbp = null; reject(unavailable()); };
      });
    }
    return dbp;
  }

  // fn({s, f, set, fail}): s = samples store, f = folders store, set(v) = the result,
  // fail(err) aborts the transaction and rejects with err. Errors reported by a failed
  // request (a duplicate name) arrive on the request, not the transaction.
  async function run(mode, fn, existsMsg) {
    const db = await open();
    return new Promise((resolve, reject) => {
      const t = db.transaction([STORE, FOLDERS], mode);
      let out, failure = null;
      const abort = (err) => { failure = err; try { t.abort(); } catch { /* already finished */ } };
      fn({ s: t.objectStore(STORE), f: t.objectStore(FOLDERS), set: (v) => { out = v; }, fail: abort });
      t.oncomplete = () => resolve(out);
      t.onerror = (e) => { if (!failure) failure = mapErr((e.target && e.target.error) || t.error, existsMsg); };
      t.onabort = () => reject(failure || mapErr(t.error, existsMsg));
    });
  }

  const byteLength = (s) => new TextEncoder().encode(s).length;
  const meta = (r) => ({ id: r.id, name: r.name, tag: r.tag || "", folder: r.folder || "", bytes: r.bytes, updatedAt: r.updatedAt, lastUsedAt: r.lastUsedAt });
  const folderRange = (key) => IDBKeyRange.bound([key, ""], [key, "\uffff"]);
  const byLower = (a, b) => a.toLowerCase().localeCompare(b.toLowerCase());

  // crypto.randomUUID needs a secure context, and a team server on plain http
  // over the LAN is not one. getRandomValues works everywhere.
  function newID() {
    const b = crypto.getRandomValues(new Uint8Array(16));
    return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  }

  function uniqueIn(taken, base) {
    const lower = new Set(taken.map((n) => n.toLowerCase()));
    if (!lower.has(base.toLowerCase())) return base;
    for (let i = 2; ; i++) {
      const suffix = ` (${i})`;
      const cand = [...base].slice(0, 80 - suffix.length).join("").trimEnd() + suffix;
      if (!lower.has(cand.toLowerCase())) return cand;
    }
  }

  // Finds the folder's stored spelling (case-insensitive), creating it if new, then calls cb with it.
  function withFolder(f, folder, cb) {
    if (!folder) { cb(""); return; }
    const key = folder.toLowerCase();
    f.get(key).onsuccess = (e) => {
      const ex = e.target.result;
      if (ex) { cb(ex.name); return; }
      f.add({ key, name: folder });
      cb(folder);
    };
  }

  window.soapvizStore = {
    available,
    byteLength,
    list: () => run("readonly", ({ s, set }) => { s.getAll().onsuccess = (e) => set(e.target.result.map(meta)); }),
    // Everything the menu shows: sample metadata and all folder names (including empty ones).
    listAll: () => run("readonly", ({ s, f, set }) => {
      const out = {};
      s.getAll().onsuccess = (e) => { out.samples = e.target.result.map(meta); };
      f.getAll().onsuccess = (e) => { out.folders = e.target.result.map((x) => x.name).sort(byLower); set(out); };
    }),
    // Full records for export.
    all: () => run("readonly", ({ s, f, set }) => {
      const out = {};
      s.getAll().onsuccess = (e) => { out.samples = e.target.result; };
      f.getAll().onsuccess = (e) => { out.folders = e.target.result.map((x) => x.name).sort(byLower); set(out); };
    }),
    get: (id) => run("readonly", ({ s, set }) => { s.get(id).onsuccess = (e) => set(e.target.result || null); }),

    create: ({ name, tag, folder, xml }) => run("readwrite", ({ s, f, set }) => {
      withFolder(f, folder, (canon) => {
        const now = Date.now();
        const rec = { id: newID(), name, nameKey: name.toLowerCase(), folder: canon, folderKey: canon.toLowerCase(), tag: tag || "", xml,
          bytes: byteLength(xml), createdAt: now, updatedAt: now, lastUsedAt: now };
        s.add(rec);
        set(meta(rec));
      });
    }),

    // patch may hold name, tag, folder and xml. Returns the new metadata, or null if the sample is gone.
    update: (id, patch) => run("readwrite", ({ s, f, set }) => {
      s.get(id).onsuccess = (e) => {
        const rec = e.target.result;
        if (!rec) { set(null); return; }
        const apply = (canon) => {
          if (patch.name !== undefined) { rec.name = patch.name; rec.nameKey = patch.name.toLowerCase(); }
          if (patch.tag !== undefined) rec.tag = patch.tag;
          if (canon !== undefined) { rec.folder = canon; rec.folderKey = canon.toLowerCase(); }
          if (patch.xml !== undefined) { rec.xml = patch.xml; rec.bytes = byteLength(patch.xml); }
          rec.updatedAt = Date.now();
          s.put(rec);
          set(meta(rec));
        };
        if (patch.folder === undefined) apply(undefined);
        else withFolder(f, patch.folder, apply);
      };
    }),

    touch: (id) => run("readwrite", ({ s }) => {
      s.get(id).onsuccess = (e) => {
        const rec = e.target.result;
        if (rec) { rec.lastUsedAt = Date.now(); s.put(rec); }
      };
    }),
    remove: (id) => run("readwrite", ({ s }) => { s.delete(id); }),

    createFolder: (name) => run("readwrite", ({ f, set }) => {
      f.add({ key: name.toLowerCase(), name });
      set(name);
    }, "A folder with that name already exists."),

    // Renames a folder (a case-only change is fine); its samples go with it.
    renameFolder: (oldName, newName) => run("readwrite", ({ s, f, set, fail: abort }) => {
      const oldKey = oldName.toLowerCase(), newKey = newName.toLowerCase();
      f.get(oldKey).onsuccess = (e) => {
        if (!e.target.result) { abort(fail("That folder no longer exists.", "gone")); return; }
        const proceed = () => {
          s.index("folderName").getAll(folderRange(oldKey)).onsuccess = (ev) => {
            for (const rec of ev.target.result) s.put({ ...rec, folder: newName, folderKey: newKey });
            f.delete(oldKey);
            f.put({ key: newKey, name: newName });
            set(newName);
          };
        };
        if (newKey === oldKey) { proceed(); return; }
        f.get(newKey).onsuccess = (e2) => {
          if (e2.target.result) abort(fail("A folder with that name already exists.", "exists"));
          else proceed();
        };
      };
    }),

    // Deletes a folder. Its samples are not deleted: they move to the top level, renamed
    // "Name (2)" if the top level already has that name. Resolves to the number moved.
    deleteFolder: (name) => run("readwrite", ({ s, f, set, fail: abort }) => {
      const key = name.toLowerCase();
      f.get(key).onsuccess = (e) => {
        if (!e.target.result) { abort(fail("That folder no longer exists.", "gone")); return; }
        s.index("folderName").getAll(folderRange(key)).onsuccess = (ev) => {
          const inside = ev.target.result;
          s.index("folderName").getAll(folderRange("")).onsuccess = (ev2) => {
            const taken = ev2.target.result.map((r) => r.name);
            for (const rec of inside) {
              const nm = uniqueIn(taken, rec.name);
              taken.push(nm);
              s.put({ ...rec, name: nm, nameKey: nm.toLowerCase(), folder: "", folderKey: "" });
            }
            f.delete(key);
            set(inside.length);
          };
        };
      };
    }),

    clear: () => run("readwrite", ({ s, f }) => { s.clear(); f.clear(); }),

    // Browsers may evict best-effort storage; ask not to. Not guaranteed, so export is the real backup.
    persist: () => (navigator.storage && navigator.storage.persist ? navigator.storage.persist().catch(() => false) : Promise.resolve(false)),
  };
})();
