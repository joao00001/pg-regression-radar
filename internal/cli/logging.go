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

package cli

import (
	"flag"
	"io"
	"log/slog"

	"github.com/joao00001/pg-regression-radar/internal/logging"
)

// installLogging builds the command's logger from --log-level and --log-format,
// makes it the process-wide slog default (so start-up errors and any component
// built without a logger write the same way), and returns it. ok is false when a
// flag value is invalid; the error has already been written.
func installLogging(f *logging.Flags, fs *flag.FlagSet, out io.Writer) (logger *slog.Logger, ok bool) {
	opts, _, err := f.Resolve(func(name string) bool {
		set := false
		fs.Visit(func(fl *flag.Flag) {
			if fl.Name == name {
				set = true
			}
		})
		return set
	}, nil)
	if err != nil {
		logging.Early(out).Error("invalid logging flag", "err", err)
		return nil, false
	}
	logger = logging.New(out, opts)
	logging.Install(logger)
	return logger, true
}
