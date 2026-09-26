package autostart

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// launchAgentLabel is the LaunchAgent's fixed identity — never derived
// from Target.Name, which is only a display string nobody but the user
// reads. Changing this after release orphans whatever was written under
// the old label on a machine that enabled it before the change
// (RELEASING.md: never change once shipped).
const launchAgentLabel = "io.github.itayp.localllmadvisor"

func darwinPlistPath(homeDir func() (string, error)) (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("autostart: home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

func darwinEnabled(homeDir func() (string, error)) (bool, error) {
	path, err := darwinPlistPath(homeDir)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("autostart: %w", err)
	}
	return true, nil
}

// darwinEnable writes the LaunchAgent plist with RunAtLoad set, so it
// takes effect from the next login onward. It deliberately does not also
// launchctl-load the agent to start the daemon right now — the process
// calling this is, in practice, already running (the click came from its
// own tray menu), so there is nothing to start.
func darwinEnable(homeDir func() (string, error), t Target) error {
	path, err := darwinPlistPath(homeDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if err := os.WriteFile(path, []byte(darwinPlist(t)), 0o644); err != nil {
		return fmt.Errorf("autostart: writing the LaunchAgent: %w", err)
	}
	return nil
}

func darwinDisable(homeDir func() (string, error)) error {
	path, err := darwinPlistPath(homeDir)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("autostart: removing the LaunchAgent: %w", err)
	}
	return nil
}

// darwinPlist renders the LaunchAgent property list by hand rather than
// through encoding/xml's struct marshalling: Apple's plist schema (repeated
// <key>/<value> pairs inside a <dict>, a bare <array> of <string>s) does
// not match how encoding/xml represents Go structs, so a template with
// each dynamic value escaped through xml.EscapeText is the straightforward
// correct approach and is what darwin_test.go parses back to check.
func darwinPlist(t Target) string {
	var args bytes.Buffer
	args.WriteString(plistString(t.ExecPath))
	for _, a := range t.Args {
		args.WriteString("\n\t\t")
		args.WriteString(plistString(a))
	}
	return fmt.Sprintf(darwinPlistTemplate, plistString(launchAgentLabel), args.String())
}

func plistString(s string) string {
	var b strings.Builder
	b.WriteString("<string>")
	_ = xml.EscapeText(&b, []byte(s))
	b.WriteString("</string>")
	return b.String()
}

const darwinPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	%s
	<key>ProgramArguments</key>
	<array>
		%s
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
