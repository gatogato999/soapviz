# soapviz

Local SOAP viewer/editor and Go struct generator. Stdlib only.

    go run ./cmd/soapviz            # opens http://127.0.0.1:8484
    go build -o soapviz ./cmd/soapviz
    ./soapviz -addr 127.0.0.1:9000 -no-open
    go test ./...

Layout: `soap/` is the core (parse, render, edit, generate) with no HTTP.
`web/` is a stateless JSON endpoint plus embedded templates and static files.

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
