// Runs before first paint so a saved theme does not flash the default one.
try {
  const p = JSON.parse(localStorage.getItem("soapviz.prefs") || "null");
  if (p && ["light", "dim", "dark"].includes(p.theme)) document.documentElement.dataset.theme = p.theme;
} catch {}
