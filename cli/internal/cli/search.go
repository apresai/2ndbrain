package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	searchType   string
	searchStatus string
	searchTag    string
	searchLimit  int
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search the vault with hybrid BM25 + semantic search",
	Args:  cobra.ArbitraryArgs,
	RunE:  runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&searchType, "type", "", "Filter by document type")
	searchCmd.Flags().StringVar(&searchStatus, "status", "", "Filter by status")
	searchCmd.Flags().StringVar(&searchTag, "tag", "", "Filter by tag")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "Maximum number of results")
	rootCmd.AddCommand(searchCmd)
}

func runSearch(cmd *cobra.Command, args []string) error {
	v, err := vault.Open(".")
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	// Ensure index exists
	var count int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&count)
	if count == 0 {
		fmt.Fprintln(os.Stderr, "notice: index empty, building now...")
		if _, err := vault.IndexVault(v, nil); err != nil {
			return fmt.Errorf("build index: %w", err)
		}
	}

	query := strings.Join(args, " ")

	// Parse inline filters from query: tag:foo type:adr status:accepted
	query, inlineType := extractPrefix(query, "type:")
	query, inlineStatus := extractPrefix(query, "status:")
	query, inlineTag := extractPrefix(query, "tag:")

	if inlineType != "" && searchType == "" {
		searchType = inlineType
	}
	if inlineStatus != "" && searchStatus == "" {
		searchStatus = inlineStatus
	}
	if inlineTag != "" && searchTag == "" {
		searchTag = inlineTag
	}

	engine := search.NewEngine(v.DB.Conn())
	results, err := engine.Search(search.Options{
		Query:  strings.TrimSpace(query),
		Type:   searchType,
		Status: searchStatus,
		Tag:    searchTag,
		Limit:  searchLimit,
	})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "No results found.")
		return output.Write(os.Stdout, getFormat(cmd), []search.Result{})
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, results)
}

// extractPrefix extracts a "prefix:value" from the query string.
func extractPrefix(query, prefix string) (string, string) {
	idx := strings.Index(query, prefix)
	if idx == -1 {
		return query, ""
	}

	rest := query[idx+len(prefix):]
	// Find end of value (next space or end of string)
	end := strings.IndexByte(rest, ' ')
	var value string
	if end == -1 {
		value = rest
		rest = ""
	} else {
		value = rest[:end]
		rest = rest[end+1:]
	}

	before := query[:idx]
	return strings.TrimSpace(before + " " + rest), value
}
