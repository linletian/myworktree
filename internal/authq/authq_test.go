package authq

import "testing"

func TestStripToken(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"directory=/x", "directory=/x"},
		{"token=secret123", ""},
		{"directory=/x&token=secret123", "directory=%2Fx"},
		{"token=secret123&session=abc", "session=abc"},
		{"session=abc&token=secret123&plan=1", "plan=1&session=abc"},
		// semicolon separators (url.Values accepts both).
		{"a=1;token=sec;b=2", "a=1;b=2"},
		// malformed %-escape: parse fails, token segments stripped
		// verbatim so the credential cannot leak.
		{"token=abc%zz&dir=x", "dir=x"},
		{"dir=x&token=abc%zz", "dir=x"},
		{"a=1;token=abc%zz;b=2", "a=1;b=2"},
		// parse failure with no recognizable token segment: drop the
		// query rather than forward a credential we cannot identify.
		{"dir=%zz&x=1", ""},
	}
	for _, c := range cases {
		if got := StripToken(c.in); got != c.want {
			t.Fatalf("StripToken(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
