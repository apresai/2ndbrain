package cli

import (
	"log/slog"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/vault"
)

// recordMetric writes one operation row to the vault's metrics sidecar
// (.2ndbrain/metrics.db). It is strictly best-effort: any failure (open or
// write) is logged at debug and swallowed, so a metrics problem can never break
// the operation it measures. A nil vault is a no-op (e.g. a command that failed
// before opening a vault). Source/CLIVersion default here when unset.
func recordMetric(v *vault.Vault, op metrics.Operation) {
	if v == nil {
		return
	}
	if op.CLIVersion == "" {
		op.CLIVersion = Version
	}
	if op.Source == "" {
		op.Source = "cli"
	}
	mdb, err := metrics.Open(filepath.Join(v.DotDir, "metrics.db"))
	if err != nil {
		slog.Debug("metrics: open failed", "err", err)
		return
	}
	defer mdb.Close()
	if err := mdb.Record(op); err != nil {
		slog.Debug("metrics: record failed", "err", err)
	}
}

// errString is err.Error() or "" for a nil error — for the metrics Error field.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
