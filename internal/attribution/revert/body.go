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

package revert

import (
	"fmt"
	"sort"
	"strings"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
)

// DiagnosisInput is what the pull request body reports.
type DiagnosisInput struct {
	App, Namespace, Revision string
	Assessment               attribution.Assessment
	Decision                 attribution.Decision
	// PriorDeploySeen is whether the radar has observed an earlier deploy of the
	// app; when it has not, the revert may remove the application's first version.
	PriorDeploySeen bool
	// IncludeQueries adds the normalised text of the affected queries. Off by
	// default: the pull request may live in a public repository and a query's
	// text names tables and columns.
	IncludeQueries bool
}

// Diagnosis writes the pull request body: why the radar thinks this deploy is
// the cause, how sure it is, and what to check before merging.
func Diagnosis(in DiagnosisInput) string {
	a, d := in.Assessment, in.Decision
	var b strings.Builder
	fmt.Fprintf(&b, "## pg-regression-radar: proposed revert\n\n")
	fmt.Fprintf(&b, "The radar found a database regression it attributes to the deploy of `%s` (revision `%s`) in `%s`, and opened this **draft** pull request to revert it. It will not merge it. Review before you do.\n\n", clean(in.App), short(in.Revision), clean(in.Namespace))

	fmt.Fprintf(&b, "### Evidence\n\n")
	fmt.Fprintf(&b, "- **Grade %s**: %s\n", d.Grade, gradeMeaning(d.Grade))
	fmt.Fprintf(&b, "- **Added database time**: at least %.1f%% (a lower bound at the radar's confidence level)\n", d.Impact*100)
	fmt.Fprintf(&b, "- **Attribution to this deploy**: %.2f (1.00 means no other deploy overlaps)\n", a.Attribution)
	fmt.Fprintf(&b, "- **Queries past the margin**: %d of %d judged\n", a.Regressed, a.Tested)
	fmt.Fprintf(&b, "- **Confirmed in %d consecutive evaluations**\n", d.Streak)
	for _, r := range d.Reasons {
		fmt.Fprintf(&b, "- %s\n", clean(r))
	}

	units := make([]attribution.UnitVerdict, 0, len(a.Units))
	for _, u := range a.Units {
		if u.Kind == attribution.KindRegressed {
			units = append(units, u)
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].ImpactShare > units[j].ImpactShare })
	if len(units) > 0 {
		fmt.Fprintf(&b, "\n### Most affected queries\n\n| Query | Slower by | 95%% range | Share of DB time before |\n|---|---|---|---|\n")
		for i, u := range units {
			if i == 5 {
				break
			}
			label := "`" + short(u.Key) + "`"
			if in.IncludeQueries {
				label += " " + "`" + snippet(u.Text) + "`"
			}
			fmt.Fprintf(&b, "| %s | %.2fx | %.2fx to %.2fx | %.1f%% |\n", label, u.Ratio, u.RatioLow, u.RatioHigh, u.ImpactShare*100)
		}
	}

	fmt.Fprintf(&b, "\n### Check before merging\n\n")
	fmt.Fprintf(&b, "- **Database migrations.** Reverting the application does not revert a schema change that shipped with it. If this change migrated the schema, confirm the previous version still works against it.\n")
	fmt.Fprintf(&b, "- **Other changes in the commit.** A revert removes everything the pull request changed, not only what slowed the database.\n")
	if !in.PriorDeploySeen {
		fmt.Fprintf(&b, "- **No earlier deploy was observed.** The radar has not seen a previous deploy of this application, so this may be its first version. Reverting it could remove the application.\n")
	}
	fmt.Fprintf(&b, "\nEvidence grades and what they mean: https://joao00001.github.io/pg-regression-radar/latest/attribution-engine/\n")
	return b.String()
}

func gradeMeaning(g attribution.Grade) string {
	switch g {
	case attribution.GradeA:
		return "compared with the same queries on an arm the deploy did not reach (the strongest evidence)"
	case attribution.GradeB:
		return "compared with a control built from queries the deploy did not touch, validated against placebo tests"
	case attribution.GradeC:
		return "compared only with each query's own history (it cannot separate the deploy from anything else that changed)"
	}
	return "not enough data"
}

func short(s string) string {
	s = clean(s)
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// clean removes what could break out of the markdown around it.
func clean(s string) string {
	r := strings.NewReplacer("`", "'", "\n", " ", "\r", " ", "|", "/", "<", "(", ">", ")", "@", "(at)", "#", "(hash)")
	return r.Replace(s)
}

func snippet(s string) string {
	s = strings.Join(strings.Fields(clean(s)), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
