// Static (GitHub Pages) build only: loads the Go engine as WebAssembly and
// plugs it in where app.js would otherwise call the server. Requires
// wasm_exec.js (from the same Go toolchain that built soapviz.wasm).
(() => {
  const go = new Go();
  const ready = (async () => {
    let inst;
    try {
      inst = await WebAssembly.instantiateStreaming(fetch("soapviz.wasm"), go.importObject);
    } catch {
      // Servers that do not send application/wasm: fall back to a plain download.
      const buf = await (await fetch("soapviz.wasm")).arrayBuffer();
      inst = await WebAssembly.instantiate(buf, go.importObject);
    }
    go.run(inst.instance); // never resolves: main() blocks forever on purpose
  })();

  window.soapvizTransport = async (req) => {
    await ready;
    return JSON.parse(window.soapvizProcess(JSON.stringify(req)));
  };

  document.addEventListener("DOMContentLoaded", () => {
    const st = document.getElementById("status");
    if (st && !st.textContent) st.textContent = "Runs in your browser. Nothing is uploaded.";
  });
})();
