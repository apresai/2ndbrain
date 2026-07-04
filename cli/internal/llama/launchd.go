package llama

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// AgentLabel is the launchd label for the always-on engine agent.
const AgentLabel = "dev.apresai.2ndbrain.engine"

// launchAgentPlistPath returns ~/Library/LaunchAgents/<label>.plist.
func launchAgentPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", AgentLabel+".plist"), nil
}

// engineLogPath returns ~/Library/Logs/2nb/engine.log (created on install).
func engineLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "2nb", "engine.log"), nil
}

// InstallAgent writes the launchd plist (running `<exePath> ai engine serve
// <extraArgs...>`) and bootstraps it so the engine starts now and at every
// login. extraArgs bakes the resolved model choices into the plist so serve is
// deterministic at login without needing an open vault. exePath should be a
// STABLE absolute path to a 2nb binary (a brew/app upgrade that moves it
// requires re-running install). macOS only.
func InstallAgent(exePath string, extraArgs []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("the always-on engine agent uses launchd (macOS only); start it manually with `2nb ai engine serve` on this platform")
	}
	if exePath == "" {
		return fmt.Errorf("install agent: empty executable path")
	}
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	logPath, err := engineLogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	plist := renderAgentPlist(exePath, logPath, extraArgs)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	// Re-bootstrap cleanly so an updated plist takes effect.
	_ = Bootout()
	return Bootstrap()
}

// UninstallAgent bootouts the agent and removes its plist.
func UninstallAgent() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	_ = Bootout()
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Bootstrap loads the agent into the user's GUI domain. Idempotent: a no-op when
// already loaded.
func Bootstrap() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("launchd is macOS only")
	}
	if AgentLoaded() {
		return nil
	}
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	return runLaunchctl("bootstrap", guiDomain(), plistPath)
}

// Bootout unloads the agent from the user's GUI domain. A "not loaded" error is
// treated as success so callers can call it unconditionally.
func Bootout() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("launchd is macOS only")
	}
	if !AgentLoaded() {
		return nil
	}
	return runLaunchctl("bootout", guiDomain()+"/"+AgentLabel)
}

// AgentLoaded reports whether the agent is currently loaded in launchd.
func AgentLoaded() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	err := exec.Command("launchctl", "print", guiDomain()+"/"+AgentLabel).Run()
	return err == nil
}

func guiDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func runLaunchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// renderAgentPlist builds the LaunchAgent plist XML. RunAtLoad + KeepAlive make
// the supervisor start at login and restart if it dies. extraArgs are appended
// to `ai engine serve` (e.g. the resolved --gen-model/--embed-model choices).
func renderAgentPlist(exePath, logPath string, extraArgs []string) string {
	var args strings.Builder
	for _, a := range extraArgs {
		args.WriteString("\n\t\t<string>")
		args.WriteString(xmlEscape(a))
		args.WriteString("</string>")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlEscape(AgentLabel) + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(exePath) + `</string>
		<string>ai</string>
		<string>engine</string>
		<string>serve</string>` + args.String() + `
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>ProcessType</key>
	<string>Adaptive</string>
</dict>
</plist>
`
}

// xmlEscape escapes the five XML predefined entities for safe inclusion in a
// plist string element.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
