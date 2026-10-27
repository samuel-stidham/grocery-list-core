package daemon

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// Render returns the service file for platform: a systemd unit for Linux, a
// launchd property list for macOS.
func Render(platform Platform, service Service) []byte {
	if platform == Darwin {
		return renderPlist(service)
	}

	return renderUnit(service)
}

// renderUnit writes the systemd user unit. The service restarts on failure
// and never on a clean stop, so systemctl stop stays stopped. The two
// hardening keys are the ones a user manager applies without privileges.
// PrivateTmp needs unprivileged user namespaces, which mainstream kernels
// allow. A hardened kernel that refuses them fails the unit at start, and
// the journal names the key.
func renderUnit(service Service) []byte {
	words := make([]string, 0, len(service.Args)+1)
	for _, word := range commandLine(service) {
		words = append(words, unitQuote(word))
	}

	text := "[Unit]\n" +
		"Description=" + unitEscape(service.Description) + "\n" +
		"\n" +
		"[Service]\n" +
		"ExecStart=" + strings.Join(words, " ") + "\n" +
		"Restart=on-failure\n" +
		"RestartSec=2\n" +
		"NoNewPrivileges=yes\n" +
		"PrivateTmp=yes\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"

	return []byte(text)
}

// plistFixedLines counts the lines of renderPlist that do not depend on the
// command line, for the one allocation.
const plistFixedLines = 16

const plistHead = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`

// renderPlist writes the launchd agent. RunAtLoad starts it when the plist
// is bootstrapped and at login. KeepAlive with SuccessfulExit false restarts
// it after a crash and leaves a clean exit alone, the launchd spelling of
// Restart=on-failure. Standard error goes to a file because launchd has no
// journal to keep the rotation alerts.
func renderPlist(service Service) []byte {
	lines := make([]string, 0, plistFixedLines+len(service.Args)+1)
	lines = append(lines,
		"\t<key>Label</key>",
		"\t<string>"+xmlText(service.Label)+"</string>",
		"\t<key>ProgramArguments</key>",
		"\t<array>",
	)

	for _, word := range commandLine(service) {
		lines = append(lines, "\t\t<string>"+xmlText(word)+"</string>")
	}

	lines = append(lines,
		"\t</array>",
		"\t<key>RunAtLoad</key>",
		"\t<true/>",
		"\t<key>KeepAlive</key>",
		"\t<dict>",
		"\t\t<key>SuccessfulExit</key>",
		"\t\t<false/>",
		"\t</dict>",
		"\t<key>StandardErrorPath</key>",
		"\t<string>"+xmlText(service.StderrLog)+"</string>",
		"</dict>",
		"</plist>",
	)

	return []byte(plistHead + strings.Join(lines, "\n") + "\n")
}

// commandLine is the binary followed by its arguments.
func commandLine(service Service) []string {
	return append([]string{service.Bin}, service.Args...)
}

// unitQuote makes one ExecStart word safe. A lone semicolon separates
// commands in an Exec value and is spelled \; to stay a word. Unit files
// expand % specifiers and $ variables inside every Exec value, so both are
// doubled. A word with whitespace, quotes, or backslashes is then wrapped in
// double quotes with systemd's C-style escapes. An empty word becomes "".
func unitQuote(word string) string {
	if word == ";" {
		return `\;`
	}

	word = strings.NewReplacer("%", "%%", "$", "$$").Replace(word)
	if word != "" && !strings.ContainsAny(word, " \t\n\"'\\") {
		return word
	}

	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(word)

	return `"` + escaped + `"`
}

// unitEscape protects a plain unit value: % is the specifier character and
// a newline would start a new key.
func unitEscape(value string) string {
	return strings.NewReplacer("%", "%%", "\n", " ").Replace(value)
}

// xmlText escapes a string for element content.
func xmlText(text string) string {
	var buf bytes.Buffer

	// EscapeText only fails when the writer does, and a buffer never does.
	_ = xml.EscapeText(&buf, []byte(text))

	return buf.String()
}
