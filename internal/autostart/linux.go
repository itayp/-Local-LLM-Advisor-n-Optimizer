package autostart

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// linuxUnitName is the systemd user unit's fixed identity — like
// launchAgentLabel and windowsRunValueName, never derived from Target.Name.
const linuxUnitName = "local-llm-advisor.service"

func linuxUnitPath(homeDir func() (string, error)) (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("autostart: home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", linuxUnitName), nil
}

// linuxUnit renders the user unit. ExecStart= is parsed by systemd with its
// own (shell-like, not shell) word-splitting rules — a value containing
// whitespace or quotes needs double-quoting, with any embedded quote or
// backslash escaped; shQuote does exactly that, nothing more (no shell is
// ever invoked to run this line).
func linuxUnit(t Target) string {
	var cmd strings.Builder
	cmd.WriteString(shQuote(t.ExecPath))
	for _, a := range t.Args {
		cmd.WriteByte(' ')
		cmd.WriteString(shQuote(a))
	}
	name := t.Name
	if name == "" {
		name = "Local LLM Advisor"
	}
	return fmt.Sprintf(linuxUnitTemplate, name, cmd.String())
}

func shQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"'$") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

const linuxUnitTemplate = `[Unit]
Description=%s

[Service]
ExecStart=%s
Restart=on-failure

[Install]
WantedBy=default.target
`

// linuxEnabled asks systemd itself, not the filesystem: the unit file could
// exist without being enabled (or vice versa is not possible, but a stray
// file with the daemon never having called Enable is), so "is-enabled" is
// the honest source of truth. Any failure to ask (systemd not running as a
// user service manager, no systemd at all) is read as "not enabled" rather
// than surfaced as an error — a checkbox that cannot be positively
// confirmed on defaults to unchecked, never to an error banner.
func linuxEnabled(ctx context.Context, run cmdRunner) (bool, error) {
	out, _ := run.run(ctx, "systemctl", "--user", "is-enabled", linuxUnitName)
	return strings.TrimSpace(out) == "enabled", nil
}

func linuxEnable(ctx context.Context, homeDir func() (string, error), run cmdRunner, t Target) error {
	path, err := linuxUnitPath(homeDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("autostart: %w", err)
	}
	if err := os.WriteFile(path, []byte(linuxUnit(t)), 0o644); err != nil {
		return fmt.Errorf("autostart: writing the systemd unit: %w", err)
	}
	if out, err := run.run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("autostart: systemctl --user daemon-reload: %w: %s", err, strings.TrimSpace(out))
	}
	if out, err := run.run(ctx, "systemctl", "--user", "enable", "--now", linuxUnitName); err != nil {
		return fmt.Errorf("autostart: systemctl --user enable --now: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func linuxDisable(ctx context.Context, run cmdRunner) error {
	if out, err := run.run(ctx, "systemctl", "--user", "disable", "--now", linuxUnitName); err != nil {
		return fmt.Errorf("autostart: systemctl --user disable --now: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}
