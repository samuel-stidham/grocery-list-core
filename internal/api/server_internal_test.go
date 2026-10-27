package api

import "testing"

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:8471": "127.0.0.1", "localhost": "localhost", "LOCALHOST:1": "localhost",
		"[::1]:8471": "::1", "[::1]": "::1", "::1": "::1", "": "", "evil.example:80": "evil.example",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
