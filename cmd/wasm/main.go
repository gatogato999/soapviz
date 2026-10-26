//go:build js && wasm

// Command wasm exposes engine.Process to JavaScript for the static
// (GitHub Pages) build. Everything runs in the visitor's browser.
package main

import (
	"encoding/json"
	"syscall/js"

	"soapviz/engine"
)

func main() {
	// soapvizProcess(requestJSON) -> responseJSON
	js.Global().Set("soapvizProcess", js.FuncOf(func(this js.Value, args []js.Value) any {
		var req engine.Request
		resp := engine.Response{Error: "bad request"}
		if len(args) == 1 && json.Unmarshal([]byte(args[0].String()), &req) == nil {
			resp = engine.Process(req)
		}
		out, err := json.Marshal(resp)
		if err != nil {
			out, _ = json.Marshal(engine.Response{Error: err.Error()})
		}
		return string(out)
	}))
	select {} // keep the module alive for later calls
}
