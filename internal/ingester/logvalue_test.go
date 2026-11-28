package ingester

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLogValue(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"plain", "checkout", "checkout"},
		{"line breaks cannot start a log line of their own", "a\nlevel=ERROR msg=forged\r\nb", "a level=ERROR msg=forged  b"},
		{"other control characters", "a\x00b\x1b[31mc\td", "a b [31mc d"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := logValue(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	got := logValue(strings.Repeat("é", 300)) // two bytes each
	if !strings.HasSuffix(got, "…") || len(got) > maxLoggedValueBytes+len("…") {
		t.Errorf("a long value is cut: length %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("a character was cut in half: %q", got)
	}
	at := strings.Repeat("a", maxLoggedValueBytes)
	if logValue(at) != at {
		t.Errorf("a value at the limit is left alone")
	}
}
