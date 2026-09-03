package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var migrateDryRun bool

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate legacy 2nb vault to Obsidian-native format",
	Long: `Upgrades a legacy vault's index database to the current schema.

A vault whose index is already at the current schema has nothing to migrate and
is reported as such. The only work this command does is the schema upgrade and
adding ".2ndbrain/" to the root .gitignore; your markdown is never modified.`,
	Example: `  2nb migrate --dry-run
  2nb migrate`,
	Args: cobra.NoArgs,
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Preview the migration changes without modifying the database")
	rootCmd.AddCommand(migrateCmd)
}

// readIndexSchemaState reports the index database's schema version and document
// count without migrating it. Both queries are surfaced: they used to be
// discarded, so an unreadable database reported as "schema v0" and every vault
// with a broken index looked like a legacy one.
func readIndexSchemaState(dbPath string) (version, docCount int, err error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer conn.Close()

	// MAX(version): the table can carry more than one row, and the migration
	// level is the highest one, not whichever row comes back first.
	if err := conn.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&version); err != nil {
		return 0, 0, fmt.Errorf("read schema version from %s: %w", dbPath, err)
	}
	if err := conn.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount); err != nil {
		return 0, 0, fmt.Errorf("count documents in %s: %w", dbPath, err)
	}
	return version, docCount, nil
}

func runMigrate(cmd *cobra.Command, args []string) error {
	// The pre-checks below are pure READS, so they resolve on the read ladder:
	// the same one every other command uses, instead of the hand-rolled
	// "--vault or cwd" this had, which ignored 2NB_VAULT and the Obsidian rung.
	// FindVaultRoot rather than vault.Open, because Open MIGRATES: calling it to
	// find out whether a migration is needed would perform the migration a
	// --dry-run promised not to. The real run resolves again, on the WRITE
	// ladder; see the comment at the bottom of this function for why the two
	// halves deliberately use different openers.
	dir, source := resolveVaultDir()
	absDir, _ := filepath.Abs(dir)
	root := vault.FindVaultRoot(absDir)
	if root == "" {
		return vaultNotFoundError(absDir, source)
	}

	dbPath := filepath.Join(root, vault.DotDirName, "index.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no index database at %s; there is nothing to migrate (run `2nb index` to build one)", dbPath)
		}
		return fmt.Errorf("stat %s: %w", dbPath, err)
	}

	version, docCount, err := readIndexSchemaState(dbPath)
	if err != nil {
		return err
	}

	if version > store.MaxSchemaVersion {
		return fmt.Errorf("vault uses schema v%d but this 2nb binary supports up to v%d; upgrade with `brew upgrade apresai/tap/twonb`", version, store.MaxSchemaVersion)
	}

	// Legacy means "behind the current schema". Any vault with an index.db used
	// to be announced as "Detected legacy database" plus "N files identified for
	// path-based mapping", so a native vault was told it needed a migration that
	// does not exist: the schema has been path-based since v1, and nothing here
	// maps anything.
	if version == store.MaxSchemaVersion {
		fmt.Printf("Vault: %s\n", root)
		fmt.Printf("Already at the current schema (v%d); nothing to migrate. %d documents indexed.\n", version, docCount)
		return nil
	}

	if migrateDryRun {
		fmt.Printf("[dry-run] Vault: %s\n", root)
		fmt.Printf("[dry-run] Legacy index database at schema v%d, %d documents indexed\n", version, docCount)
		fmt.Printf("[dry-run] Would upgrade the index schema v%d to v%d\n", version, store.MaxSchemaVersion)
		fmt.Printf("[dry-run] Would ensure \".2ndbrain/\" is listed in the root .gitignore\n")
		fmt.Printf("[dry-run] Your markdown is not modified.\n")
		return nil
	}

	// Everything above this line only READ the vault, which is why it runs on the
	// read ladder: a --dry-run preview must never write, and a vault already at
	// the current schema is answered without touching anything.
	//
	// This is where migrate becomes a write. vault.Open applies the schema
	// migrations and ensures the sidecar is ignored, and that IS the migration,
	// so it goes through the write opener like every other write: an explicit
	// --vault at a vault Obsidian does not know is refused without
	// --unconfigured, a working directory that is only a vault by walking up is
	// refused outright, and the resolved target is announced.
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()

	// Defensive. The pre-check described `root` from the read ladder; the write
	// ladder resolved `v.Root`. A migration must never run against a vault other
	// than the one just inspected, so refuse rather than report someone else's
	// schema numbers over this vault's upgrade. Note the ordering honestly: the
	// open above has already applied the migrations by the time this can fire,
	// and the only way to check earlier would be a third copy of the resolution
	// ladder, which is how ladders drift apart. The migration itself is the same
	// one any read command performs when it opens that vault, so the check is
	// about reporting the truth, not about preventing a novel mutation.
	if canonicalVaultPath(v.Root) != canonicalVaultPath(root) {
		return fmt.Errorf("refusing to report a migration of %s: the schema was checked on %s, which is a different vault; re-run with --vault %s", v.Root, root, root)
	}

	fmt.Printf("Vault: %s\n", v.Root)
	fmt.Printf("Upgraded the index schema v%d to v%d\n", version, store.MaxSchemaVersion)
	fmt.Printf("Ensured \".2ndbrain/\" is listed in the root .gitignore\n")
	fmt.Printf("Your markdown was not modified. Run \"2nb index\" to rebuild the index and refresh embeddings.\n")

	return nil
}
