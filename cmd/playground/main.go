// Copyright 2026 The pg-regression-radar Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build js && wasm

// Command playground is the WebAssembly build of the correlation engine behind
// the docs playground. It exposes one function to JavaScript:
//
//	pgrrAnalyse(requestJSON string) string
//
// which takes a playground.Request as JSON and returns a playground.Response as
// JSON. Build it with scripts/build_playground.sh.
package main

import (
	"syscall/js"

	"github.com/joao00001/pg-regression-radar/internal/playground"
)

func main() {
	js.Global().Set("pgrrAnalyse", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return `{"error":"pgrrAnalyse expects one JSON string"}`
		}
		return playground.RunJSON(args[0].String())
	}))
	js.Global().Set("pgrrReady", true)
	// Keep the Go runtime alive so the exported function stays callable.
	select {}
}
