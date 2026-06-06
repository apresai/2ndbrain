package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var migrateDryRun bool

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate legacy 2nb vault to Obsidian-native format",
	Example: `  2nb migrate --dry-run
  2nb migrate`,
	Args: cobra.NoArgs,
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Preview the migration changes without modifying the database")
	rootCmd.AddCommand(migrateCmd)
}

func runMigrate(cmd *cobra.Command, args []string) error {
	// Locate vault path
	vaultFlag, _ := cmd.Flags().GetString("vault")
	var vaultPath string
	if vaultFlag != "" {
		vaultPath = vaultFlag
	} else {
		var err error
		vaultPath, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	absVaultPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return err
	}

	dbPath := filepath.Join(absVaultPath, ".2ndbrain", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no legacy index.db found at %s", dbPath)
	}

	// 1. Read current version from database without migrating it
	dbConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer dbConn.Close()

	var version int
	_ = dbConn.QueryRow("SELECT version FROM schema_version").Scan(&version)

	var docCount int
	_ = dbConn.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)

	if migrateDryRun {
		fmt.Printf("[dry-run] Scanning legacy vault at: %s\n", absVaultPath)
		fmt.Printf("[dry-run] Detected legacy database (schema v%d)\n", version)
		fmt.Printf("[dry-run] %d files identified for path-based mapping\n", docCount)
		fmt.Printf("[dry-run] Append \".2ndbrain/\" to root .gitignore\n")
		fmt.Printf("[dry-run] Safe to proceed. No files will be modified.\n")
		return nil
	}

	fmt.Printf("Scanning legacy vault at: %s\n", absVaultPath)
	fmt.Printf("Upgrading database schema v%d to v3...", version)

	// Opening via vault.Open will run the migrations and migrate v2 -> v3
	v, err := vault.Open(absVaultPath)
	if err != nil {
		fmt.Println(" Failed")
		return fmt.Errorf("migration failed: %w", err)
	}
	defer v.Close()
	fmt.Println(" Done")

	// vault.Open ran the schema migration and ensured the sidecar is ignored;
	// it does not rewrite any markdown. Report only what actually happened.
	fmt.Printf("Ensured \".2ndbrain/\" is listed in the root .gitignore\n")
	fmt.Printf("Migration complete. Run \"2nb index\" to rebuild the index and refresh embeddings.\n")

	return nil
}
