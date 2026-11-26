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

package layout

import yaml "go.yaml.in/yaml/v3"

// migration rewrites a file of version from into one of version to, in place on
// its YAML tree, so what it leaves keeps the author's comments and line numbers.
type migration struct {
	from, to string
	apply    func(root *yaml.Node, ps *problems)
}

// migrations is the chain from older file versions to APIVersion. It is empty
// because pgrr.io/v1 is the first version there has been; when the format has to
// change incompatibly, the new version adds its step here and a test pins it, so
// files people already wrote keep loading.
var migrations []migration

// migrate brings the file's apiVersion up to APIVersion, or says why it cannot.
func migrate(root *yaml.Node, ps *problems) {
	var vn *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "apiVersion" {
			vn = root.Content[i+1]
		}
	}
	if vn == nil {
		ps.add(root, "apiVersion", "apiVersion is missing; this build reads %s", APIVersion)
		return
	}
	for steps := 0; vn.Value != APIVersion; steps++ {
		var m *migration
		for i := range migrations {
			if migrations[i].from == vn.Value {
				m = &migrations[i]
			}
		}
		if m == nil || steps > len(migrations) {
			ps.add(vn, "apiVersion", "apiVersion %q is not one this build reads (it reads %s); if the file came from a newer release, use that release's dashboard command", vn.Value, APIVersion)
			return
		}
		m.apply(root, ps)
		if len(ps.list) > 0 {
			return
		}
		vn.Value = m.to
	}
}
