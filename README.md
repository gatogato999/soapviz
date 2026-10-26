# soapviz

Local SOAP viewer/editor and Go struct generator. Stdlib only.

    go run ./cmd/soapviz            # serves http://127.0.0.1:8484, does not open a browser
    go build -o soapviz ./cmd/soapviz
    ./soapviz -addr 0.0.0.0:9000 -open -config /etc/soapviz.ini
    go test ./...

Configuration: `config.ini` (see the file; every key is optional). It is read from
`-config`, else `./config.ini`, else next to the binary. Flags `-addr` and `-open`
override it. An invalid file is a startup error; unknown keys are errors too.

Shared server: the server keeps no per-user state. Theme, Go options and pane
widths are saved in each developer's own browser (localStorage); `config.ini`
only supplies the defaults for people who have not changed anything. "Reset to
defaults" in the header clears a browser's saved choices. There is no
authentication, so bind to 0.0.0.0 only on a network you trust.

Layout
- `soap/`    core: parse, render, edit, generate. Standard library only.
- `engine/`  one request/response function (`Process`) over the core, plus the Go highlighter. No HTTP, no templates.
- `web/`     HTTP server, embedded UI files. Stateless.
- `cmd/soapviz`  the server. `cmd/wasm`  the same engine compiled to WebAssembly.
- `pages/`, `scripts/build-pages.sh`, `.github/workflows/pages.yml`  static build for GitHub Pages.

The UI (`web/static`, `web/templates/index.html`) is shared. It calls `api/process`
on the server, or `window.soapvizTransport` (WebAssembly) in the static build.

GitHub Pages (free plan needs a public repository)
1. Push to GitHub, default branch `main`.
2. Settings > Pages > Source: **GitHub Actions**.
3. Each push to `main` runs vet and tests, builds `dist/`, and deploys it.
   The site is `https://<user>.github.io/<repo>/`. Everything runs in the visitor's
   browser; nothing is uploaded. Build locally with `scripts/build-pages.sh /tmp/site`
   and serve that folder with any static file server. `SOAPVIZ_THEME=dark` sets the
   first-visit theme. `config.ini` does not apply to the static build.
Do not commit real bank payloads or hostnames to a public repository. Add a LICENSE
if you want others to be allowed to use the code.

UI
- Three themes (light, dim, dark) via the swatches in the header.
- Pane widths: drag the handles, or focus one and use Left/Right (Shift = bigger steps). Double-click resets.
- XML is highlighted client-side (textarea over a highlighted copy); Go is highlighted server-side with go/scanner.

Generator rules
- Same-named siblings merge across all occurrences: repeated -> `[]T`, missing in some -> optional.
- Leaf without attributes -> `string`. No type guessing (leading zeros, "true" etc. stay strings).
- Leaf with attributes -> struct with `Value` (`,chardata`).
- Optional struct -> `*T` with `omitempty`; optional string -> `string` with `omitempty`.
- Name clash with a different shape -> `ParentName`, then numbered.
- Namespace URIs go into xml tags only if the option is on; off matches by local name.

Known limits
- Comments, processing instructions and the XML declaration are dropped on edit/render.
- Whitespace around text is trimmed.
