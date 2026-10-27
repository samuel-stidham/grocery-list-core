package daemon

import "testing"

func TestUnitQuote(t *testing.T) {
	cases := map[string]string{
		"plain":           "plain",
		"/usr/bin/fdc":    "/usr/bin/fdc",
		"has space":       `"has space"`,
		"tab\there":       `"tab\there"`,
		"line\nbreak":     `"line\nbreak"`,
		"100%":            "100%%",
		"$HOME/x":         "$$HOME/x",
		`a"b`:             `"a\"b"`,
		`back\slash`:      `"back\\slash"`,
		"it's":            `"it's"`,
		"":                `""`,
		"50% of $x here":  `"50%% of $$x here"`,
		"/home/test user": `"/home/test user"`,
	}

	for word, want := range cases {
		if got := unitQuote(word); got != want {
			t.Errorf("unitQuote(%q) = %s, want %s", word, got, want)
		}
	}
}

func TestUnitEscapeAndXMLText(t *testing.T) {
	if got := unitEscape("100% done\nnext"); got != "100%% done next" {
		t.Fatalf("unitEscape = %q", got)
	}

	if got := xmlText(`a & b <c> "d"`); got != "a &amp; b &lt;c&gt; &#34;d&#34;" {
		t.Fatalf("xmlText = %q", got)
	}
}
