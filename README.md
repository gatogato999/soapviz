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

Layout: `soap/` is the core (parse, render, edit, generate) with no HTTP.
`web/` is a stateless JSON endpoint plus embedded templates and static files.

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
