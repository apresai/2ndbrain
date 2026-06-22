package cli

import (
	"errors"
	"strings"
	"testing"
)

// deriveSuiteStatus is the pure parity core. These cover the CLI version-compare
// cases (update-available, up-to-date, ahead, dev build, offline) plus the
// app/plugin presence states the suite view adds.
func TestDeriveSuiteStatus_CLI(t *testing.T) {
	// appApplicable=false and vaultKnown=false isolate the CLI assertions.
	cli := func(cliVer, latest string, fetchErr error) ProductState {
		return deriveSuiteStatus(cliVer, "", false, "", false, latest, fetchErr).CLI
	}

	t.Run("update available", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.0", "", false, "", false, "v0.10.1", nil)
		if !s.Checked || s.Latest != "v0.10.1" {
			t.Fatalf("got %+v, want checked + latest v0.10.1", s)
		}
		if s.CLI.Status != statusOutdated || !s.CLI.UpdateAvailable || s.CLI.Fix != fixCLIUpgrade {
			t.Errorf("CLI = %+v, want outdated + update + fix", s.CLI)
		}
		if s.InSync {
			t.Error("InSync should be false when the CLI is behind")
		}
	})
	t.Run("up to date", func(t *testing.T) {
		p := cli("0.10.1", "v0.10.1", nil)
		if p.Status != statusOK || p.UpdateAvailable {
			t.Errorf("got %+v, want ok + no update", p)
		}
	})
	t.Run("local build newer than latest is not an update", func(t *testing.T) {
		p := cli("0.11.0", "v0.10.1", nil)
		if p.Status != statusOK || p.UpdateAvailable {
			t.Errorf("got %+v, want ok (CLI ahead), no update", p)
		}
	})
	t.Run("dev build is not comparable", func(t *testing.T) {
		s := deriveSuiteStatus("dev", "", false, "", false, "v0.10.1", nil)
		if !s.Checked || s.CLI.Status != statusUnknown || s.CLI.UpdateAvailable {
			t.Errorf("CLI = %+v, want unknown, no update, checked", s.CLI)
		}
		if s.Detail == "" {
			t.Error("want a detail explaining the dev build isn't comparable")
		}
	})
	t.Run("fetch error means not checked", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.1", "", false, "", false, "", errors.New("offline"))
		if s.Checked || s.CLI.UpdateAvailable || s.Detail == "" {
			t.Errorf("got %+v, want unchecked CLI with a detail", s)
		}
		if s.CLI.Status != statusUnknown {
			t.Errorf("CLI status = %q, want unknown when offline", s.CLI.Status)
		}
		if s.InSync {
			t.Error("InSync must be false when the check failed")
		}
	})
}

func TestDeriveSuiteStatus_AppAndPlugin(t *testing.T) {
	t.Run("app outdated, plugin current -> not in sync", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "0.10.3", true, "0.10.4", true, "v0.10.4", nil)
		if s.App.Status != statusOutdated || s.App.Fix != fixAppUpgrade {
			t.Errorf("App = %+v, want outdated + upgrade fix", s.App)
		}
		if s.Plugin.Status != statusOK {
			t.Errorf("Plugin = %+v, want ok", s.Plugin)
		}
		if s.InSync {
			t.Error("InSync should be false when the app is behind")
		}
	})

	t.Run("everything current -> in sync", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "0.10.4", true, "0.10.4", true, "v0.10.4", nil)
		if !s.InSync {
			t.Errorf("want InSync, got %+v", s)
		}
		if s.CLI.Status != statusOK || s.App.Status != statusOK || s.Plugin.Status != statusOK {
			t.Errorf("want all ok, got cli=%q app=%q plugin=%q", s.CLI.Status, s.App.Status, s.Plugin.Status)
		}
	})

	t.Run("app not installed -> missing with install fix, still in sync", func(t *testing.T) {
		// Missing (not behind) does not flip InSync: a CLI-only user isn't "out of sync".
		s := deriveSuiteStatus("0.10.4", "", true, "0.10.4", true, "v0.10.4", nil)
		if s.App.Status != statusMissing || s.App.Installed || s.App.Fix != fixAppInstall {
			t.Errorf("App = %+v, want missing + install fix, not installed", s.App)
		}
		if !s.InSync {
			t.Error("InSync should stay true when a component is merely not installed")
		}
	})

	t.Run("app not applicable off darwin", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "", false, "0.10.4", true, "v0.10.4", nil)
		if s.App.Status != statusNA || s.App.Installed {
			t.Errorf("App = %+v, want n/a", s.App)
		}
		if !s.InSync {
			t.Error("a not-applicable app must not flip InSync")
		}
	})

	t.Run("vault unknown -> plugin unknown, not behind", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "0.10.4", true, "", false, "v0.10.4", nil)
		if s.Plugin.Status != statusUnknown || s.Plugin.Fix == "" {
			t.Errorf("Plugin = %+v, want unknown with a hint", s.Plugin)
		}
		if !s.InSync {
			t.Error("an unverifiable plugin must not flip InSync")
		}
	})

	t.Run("plugin behind the CLI -> plugin outdated", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "0.10.4", true, "0.10.3", true, "v0.10.4", nil)
		if s.Plugin.Status != statusOutdated || s.Plugin.Fix != fixPluginInstall {
			t.Errorf("Plugin = %+v, want outdated + install fix", s.Plugin)
		}
		if s.InSync {
			t.Error("InSync should be false when the plugin is behind")
		}
	})
}

func TestCFBundleShortVersion(t *testing.T) {
	// The exact XML plist the Makefile writes for the app bundle.
	plist := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>SecondBrain</string>
<key>CFBundleIdentifier</key><string>dev.apresai.2ndbrain</string>
<key>CFBundleShortVersionString</key><string>0.10.3</string>
<key>CFBundleVersion</key><string>0.10.3</string>
</dict></plist>`)
	if v, ok := cfBundleShortVersion(plist); !ok || v != "0.10.3" {
		t.Errorf("cfBundleShortVersion = (%q, %v), want (0.10.3, true)", v, ok)
	}

	t.Run("missing key", func(t *testing.T) {
		if v, ok := cfBundleShortVersion([]byte(`<plist><dict></dict></plist>`)); ok {
			t.Errorf("want ok=false on a plist without the key, got %q", v)
		}
	})
}

func TestOutdatedProducts(t *testing.T) {
	s := deriveSuiteStatus("0.10.4", "0.10.3", true, "0.10.3", true, "v0.10.4", nil)
	behind := outdatedProducts(s)
	if len(behind) != 2 {
		t.Fatalf("want 2 outdated (app, plugin), got %d: %+v", len(behind), behind)
	}
	if behind[0].Name != "app" || behind[1].Name != "plugin" {
		t.Errorf("want [app plugin], got [%s %s]", behind[0].Name, behind[1].Name)
	}
}

func TestSuiteVerdict(t *testing.T) {
	t.Run("offline returns the detail", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "", false, "", false, "", errors.New("offline"))
		if got := suiteVerdict(s); got != s.Detail || got == "" {
			t.Errorf("verdict = %q, want the offline detail", got)
		}
	})
	t.Run("components behind are named", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "0.10.3", true, "0.10.3", true, "v0.10.4", nil)
		got := suiteVerdict(s)
		if !strings.Contains(got, "behind") || !strings.Contains(got, "app") || !strings.Contains(got, "plugin") {
			t.Errorf("verdict = %q, want it to name app+plugin as behind", got)
		}
	})
	t.Run("all in sync", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "0.10.4", true, "0.10.4", true, "v0.10.4", nil)
		got := suiteVerdict(s)
		if !strings.Contains(got, "in sync") || !strings.Contains(got, "v0.10.4") {
			t.Errorf("verdict = %q, want an in-sync line at v0.10.4", got)
		}
	})
	t.Run("nothing behind but plugin not checked is noted", func(t *testing.T) {
		s := deriveSuiteStatus("0.10.4", "0.10.4", true, "", false, "v0.10.4", nil)
		got := suiteVerdict(s)
		if !strings.Contains(got, "in sync") || !strings.Contains(got, "plugin not checked") {
			t.Errorf("verdict = %q, want in-sync with a plugin-not-checked note", got)
		}
	})
}

func TestFormatProductRow(t *testing.T) {
	cases := []struct {
		name string
		p    ProductState
		want []string // substrings the row must contain
	}{
		{"ok", ProductState{Name: "cli", Status: statusOK, Installed: true, Version: "0.10.4"}, []string{"0.10.4", "up to date"}},
		{"outdated", ProductState{Name: "app", Status: statusOutdated, Installed: true, Version: "0.10.3", Fix: fixAppUpgrade}, []string{"0.10.3", "outdated", fixAppUpgrade}},
		{"missing", ProductState{Name: "app", Status: statusMissing, Fix: fixAppInstall}, []string{"—", "not installed", fixAppInstall}},
		{"n/a", ProductState{Name: "app", Status: statusNA}, []string{"—", "macOS only"}},
		{"unknown with hint", ProductState{Name: "plugin", Status: statusUnknown, Fix: "open a vault"}, []string{"—", "open a vault"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatProductRow("Label", tc.p)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("row %q missing %q", got, want)
				}
			}
		})
	}
}

// updateStatusFromSuite is the bridge to the historical `2nb update --json`
// contract; this locks the field mapping the deleted TestBuildUpdateStatus used
// to guard.
func TestUpdateStatusFromSuite(t *testing.T) {
	s := deriveSuiteStatus("0.10.0", "0.10.3", true, "0.10.4", true, "v0.10.4", nil)
	u := updateStatusFromSuite(s)
	if u.Current != "0.10.0" || u.Latest != "v0.10.4" || u.Checked != true {
		t.Errorf("got current=%q latest=%q checked=%v, want CLI fields mirrored", u.Current, u.Latest, u.Checked)
	}
	if !u.UpdateAvailable {
		t.Error("UpdateAvailable must reflect the CLI being behind")
	}
	if u.App.Name != "app" || u.Plugin.Name != "plugin" {
		t.Errorf("app/plugin states not carried through: app=%+v plugin=%+v", u.App, u.Plugin)
	}
}
