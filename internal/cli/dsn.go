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
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

var keyValueDSNPattern = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_]*=`)

func validatePostgresDSN(dsn string) error {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return fmt.Errorf("DSN is empty")
	}

	if keyValueDSNPattern.MatchString(dsn) {
		if _, err := pq.NewConnector(dsn); err != nil {
			return fmt.Errorf("parse Postgres connection string: %w", err)
		}
		return nil
	}

	lower := strings.ToLower(dsn)
	if strings.Contains(dsn, "://") ||
		strings.HasPrefix(lower, "postgres:") ||
		strings.HasPrefix(lower, "postgresql:") {
		return validatePostgresURI(dsn)
	}

	if strings.Contains(dsn, "@") {
		return fmt.Errorf("parse Postgres DSN: userinfo requires an explicit postgres:// or postgresql:// URI")
	}

	return validatePostgresURI("postgres://" + dsn)
}

func validatePostgresURI(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("parse Postgres URI: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "postgres", "postgresql":
	default:
		return fmt.Errorf("parse Postgres URI: invalid scheme %q (want postgres or postgresql)", u.Scheme)
	}

	if u.Opaque != "" {
		return fmt.Errorf("parse Postgres URI: opaque URI form is not supported; use postgres://host:port/dbname")
	}

	if u.User != nil && u.User.Username() == "" {
		return fmt.Errorf("parse Postgres URI: empty username in userinfo")
	}

	if u.Host == "" && u.Query().Get("host") == "" {
		return fmt.Errorf("parse Postgres URI: missing host")
	}

	if _, err := pq.ParseURL(dsn); err != nil {
		return fmt.Errorf("parse Postgres URI: %w", err)
	}

	return nil
}
