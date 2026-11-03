"use strict";
// "My samples": named XML samples kept in this browser's IndexedDB. Nothing
// here talks to the server. IndexedDB (not localStorage) because samples can be
// megabytes and localStorage is capped near 5 MB and blocks the page.
(() => {
  const DB = "soapviz", STORE = "samples", VERSION = 1;
  const available = typeof indexedDB !== "undefined";
  let dbp = null;

  function fail(message, code) {
    return Object.assign(new Error(message), { code });
  }

  function mapErr(err) {
    const n = err && err.name;
    if (n === "ConstraintError") return fail("A sample with that name already exists.", "exists");
    if (n === "QuotaExceededError") return fail("Browser storage is full. Export and delete some samples.", "quota");
    return err instanceof Error ? err : fail("Browser storage failed.", "storage");
  }

  function open() {
    if (!available) return Promise.reject(fail("Saving in this browser is not available (private mode or blocked storage).", "unavailable"));
    if (!dbp) {
      dbp = new Promise((resolve, reject) => {
        let r;
        try { r = indexedDB.open(DB, VERSION); } catch (e) { dbp = null; reject(mapErr(e)); return; }
        r.onupgradeneeded = () => {
          const s = r.result.createObjectStore(STORE, { keyPath: "id" });
          s.createIndex("nameKey", "nameKey", { unique: true }); // names are unique ignoring case
        };
        r.onsuccess = () => resolve(r.result);
        r.onerror = () => { dbp = null; reject(fail("Saving in this browser is not available (private mode or blocked storage).", "unavailable")); };
      });
    }
    return dbp;
  }

  async function run(mode, fn) {
    const db = await open();
    return new Promise((resolve, reject) => {
      const t = db.transaction(STORE, mode);
      let out;
      fn(t.objectStore(STORE), (v) => { out = v; });
      t.oncomplete = () => resolve(out);
      // A failed request (e.g. a duplicate name) reports its error on the request;
      // t.error is only filled in later, on abort. The first rejection wins.
      t.onerror = (e) => reject(mapErr((e.target && e.target.error) || t.error));
      t.onabort = () => reject(mapErr(t.error));
    });
  }

  const byteLength = (s) => new TextEncoder().encode(s).length;
  const meta = (r) => ({ id: r.id, name: r.name, tag: r.tag || "", bytes: r.bytes, updatedAt: r.updatedAt, lastUsedAt: r.lastUsedAt });

  // crypto.randomUUID needs a secure context, and a team server on plain http
  // over the LAN is not one. getRandomValues works everywhere.
  function newID() {
    const b = crypto.getRandomValues(new Uint8Array(16));
    return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  }

  window.soapvizStore = {
    available,
    list: () => run("readonly", (s, set) => { s.getAll().onsuccess = (e) => set(e.target.result.map(meta)); }),
    all: () => run("readonly", (s, set) => { s.getAll().onsuccess = (e) => set(e.target.result); }),
    get: (id) => run("readonly", (s, set) => { s.get(id).onsuccess = (e) => set(e.target.result || null); }),
    create: ({ name, tag, xml }) => run("readwrite", (s, set) => {
      const now = Date.now();
      const rec = { id: newID(), name, nameKey: name.toLowerCase(), tag: tag || "", xml, bytes: byteLength(xml), createdAt: now, updatedAt: now, lastUsedAt: now };
      s.add(rec);
      set(meta(rec));
    }),
    // patch may hold name, tag and xml. Returns the new metadata, or null if the sample is gone.
    update: (id, patch) => run("readwrite", (s, set) => {
      s.get(id).onsuccess = (e) => {
        const rec = e.target.result;
        if (!rec) { set(null); return; }
        if (patch.name !== undefined) { rec.name = patch.name; rec.nameKey = patch.name.toLowerCase(); }
        if (patch.tag !== undefined) rec.tag = patch.tag;
        if (patch.xml !== undefined) { rec.xml = patch.xml; rec.bytes = byteLength(patch.xml); }
        rec.updatedAt = Date.now();
        s.put(rec);
        set(meta(rec));
      };
    }),
    touch: (id) => run("readwrite", (s) => {
      s.get(id).onsuccess = (e) => {
        const rec = e.target.result;
        if (rec) { rec.lastUsedAt = Date.now(); s.put(rec); }
      };
    }),
    remove: (id) => run("readwrite", (s) => { s.delete(id); }),
    clear: () => run("readwrite", (s) => { s.clear(); }),
    // Browsers may evict best-effort storage; ask not to. Not guaranteed, so export is the real backup.
    persist: () => (navigator.storage && navigator.storage.persist ? navigator.storage.persist().catch(() => false) : Promise.resolve(false)),
    byteLength,
  };
})();
