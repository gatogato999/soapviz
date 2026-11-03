"use strict";
// Sample tags: 1 to 4 letters, shown as a colored chip. A tag's color comes
// from the server config when set, then the built-in defaults, and otherwise
// from a hash of the tag, so the same tag has the same color for everyone.
(() => {
  const PALETTE = ["yellow", "blue", "green", "red", "purple", "orange", "teal", "gray"];
  // Keep in sync with config.Default() in config/config.go.
  const DEFAULTS = { req: "yellow", res: "blue", err: "red", ok: "green" };
  const TAG_RE = /^[A-Za-z]{1,4}$/;
  let overrides = {};

  function valid(tag) {
    return tag === "" || TAG_RE.test(tag);
  }

  function hash(s) { // FNV-1a
    let h = 0x811c9dc5;
    for (let i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    return h >>> 0;
  }

  function color(tag) {
    const k = tag.toLowerCase();
    return overrides[k] || DEFAULTS[k] || PALETTE[hash(k) % PALETTE.length];
  }

  function setOverrides(map) {
    overrides = {};
    for (const [k, v] of Object.entries(map || {})) {
      if (PALETTE.includes(v) && /^[a-z]{1,4}$/.test(k)) overrides[k] = v;
    }
  }

  // Returns a <span> chip, or null for an empty tag. Text goes in via textContent.
  function chip(tag) {
    if (!tag) return null;
    const e = document.createElement("span");
    e.className = "chip";
    e.dataset.c = color(tag);
    e.textContent = tag;
    e.title = `Tag: ${tag}`;
    return e;
  }

  window.soapvizTags = { PALETTE, valid, color, setOverrides, chip };
})();
