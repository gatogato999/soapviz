// Runs before first paint so a saved theme or pane choice does not flash the default.
try {
  const p = JSON.parse(localStorage.getItem("soapviz.prefs") || "null");
  if (p && ["light", "dim", "dark"].includes(p.theme)) document.documentElement.dataset.theme = p.theme;
  if (p && p.view && p.view.xml === false) document.documentElement.dataset.hideXml = "";
  if (p && p.view && p.view.tree === false) document.documentElement.dataset.hideTree = "";
} catch {}
