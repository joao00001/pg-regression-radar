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

// Package layout is the dashboard's file format: which boards there are and
// which panels sit where on each. It owns the one list of panel types, so the
// validator, the presets, `dashboard config dump` and the UI (which is handed the
// catalog) cannot disagree about what exists, how big it may be or what it takes.
//
// A dashboard file is YAML; the UI is served the validated result as JSON, so it
// never needs a YAML parser. Panels sit on a fixed grid: 12 columns, rows of
// fixed height, each panel `at: [x, y, w, h]` in cells.
package layout

import "fmt"

const (
	// APIVersion is the version of the file format this build writes and reads.
	APIVersion = "pgrr.io/v1"
	// Kind is the only kind of file there is.
	Kind = "Dashboard"

	// Columns is the width of the grid, in cells.
	Columns = 12
	// MaxRows bounds a panel's height and a board's depth, in cells.
	MaxRows = 60

	MaxBoards         = 12
	MaxPanelsPerBoard = 40
)

// Dashboard is a whole file.
type Dashboard struct {
	APIVersion string  `json:"apiVersion"`
	Kind       string  `json:"kind"`
	Boards     []Board `json:"boards"`
}

// Board is one screen of panels: "Now", "Case", "Source health" in the presets.
type Board struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Range is how far back the board's charts look by default: a number and
	// m, h or d ("90m", "6h", "7d"). The time picker can change it on screen.
	Range  string  `json:"range"`
	Panels []Panel `json:"panels"`
}

// Panel is one thing on a board.
type Panel struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// At is [x, y, w, h] in grid cells.
	At [4]int `json:"at"`
	// Title replaces the type's title in the panel's header.
	Title string `json:"title,omitempty"`
	// Description is what the info button on the panel says; it replaces the
	// panel type's own help text.
	Description string `json:"description,omitempty"`
	// Query says which of the served data the panel shows; see ParseQuery.
	Query   string         `json:"query,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

// MaxDescription is the longest panel description, in characters: a few sentences.
const MaxDescription = 300

// DefaultRange is a board's range when the file does not say.
const DefaultRange = "6h"

// Problem is one thing wrong with a file, where it is, and what to do.
type Problem struct {
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Path string `json:"path,omitempty"`
	Msg  string `json:"message"`
}

func (p Problem) String() string {
	if p.Line == 0 {
		return p.Msg
	}
	return fmt.Sprintf("%d:%d: %s", p.Line, p.Col, p.Msg)
}
