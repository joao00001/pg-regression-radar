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

// Package docscheck holds tests that keep the troubleshooting pages honest. It
// has no code: the tests read the Go sources and the Markdown under
// docs/troubleshooting and fail when they disagree, so a log message, a reason
// code or a metric that exists in one and not the other is caught in CI instead
// of by a person who followed an out-of-date page at three in the morning.
package docscheck
