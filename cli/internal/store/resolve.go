package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrTargetNotFound is returned by ResolveTarget when no document matches the
// supplied name through any resolution tier.
var ErrTargetNotFound = errors.New("no document matches target")

// AmbiguousTargetError is returned by ResolveTarget when a resolution tier
// matches more than one document. It carries the candidate vault-relative paths
// so the caller can surface them and refuse to guess (no silent pick, no write).
type AmbiguousTargetError struct {
	Name       string
	Candidates []string
}

func (e *AmbiguousTargetError) Error() string {
	return fmt.Sprintf("ambiguous target %q matches %d documents: %s",
		e.Name, len(e.Candidates), strings.Join(e.Candidates, ", "))
}

// lookupIndex holds the in-memory resolution structures shared by ResolveLinks
// (wikilink resolution) and ResolveTarget (CLI target resolution). Building it
// once is O(docs + names) and lets both callers do O(1) tiered lookups.
type lookupIndex struct {
	exactPaths map[string]string              // full vault-relative path -> docID (path is UNIQUE)
	nameIndex  map[string]map[string]struct{} // every "/"-delimited path suffix + basename -> set of docIDs
	titles     map[string][]string            // title -> docIDs
	aliases    map[string][]string            // alias -> docIDs
	pathByID   map[string]string              // docID -> path (for candidate reporting)
}

// fetchDocInfos loads (id, path, title) for every document.
func (db *DB) fetchDocInfos() ([]docInfo, error) {
	rows, err := db.conn.Query("SELECT id, path, title FROM documents")
	if err != nil {
		return nil, fmt.Errorf("query docs: %w", err)
	}
	defer rows.Close()

	var docs []docInfo
	for rows.Next() {
		var d docInfo
		if err := rows.Scan(&d.id, &d.path, &d.title); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// fetchAliases loads alias -> docIDs for every alias.
func (db *DB) fetchAliases() (map[string][]string, error) {
	rows, err := db.conn.Query("SELECT doc_id, alias FROM aliases")
	if err != nil {
		return nil, fmt.Errorf("query aliases: %w", err)
	}
	defer rows.Close()

	aliases := make(map[string][]string)
	for rows.Next() {
		var docID, alias string
		if err := rows.Scan(&docID, &alias); err != nil {
			return nil, err
		}
		aliases[alias] = append(aliases[alias], docID)
	}
	return aliases, rows.Err()
}

// buildLookupIndex constructs the shared resolution index. The nameIndex maps
// every "/"-delimited path suffix (the basename is the shortest) and the full
// path, with and without the .md extension, to the set of docIDs that carry it,
// which gives shortest-unique-suffix resolution in O(1) lookups.
func buildLookupIndex(docs []docInfo, aliases map[string][]string) *lookupIndex {
	idx := &lookupIndex{
		exactPaths: make(map[string]string),
		nameIndex:  make(map[string]map[string]struct{}),
		titles:     make(map[string][]string),
		aliases:    aliases,
		pathByID:   make(map[string]string),
	}

	addName := func(name, id string) {
		if name == "" {
			return
		}
		set := idx.nameIndex[name]
		if set == nil {
			set = make(map[string]struct{})
			idx.nameIndex[name] = set
		}
		set[id] = struct{}{}
	}

	for _, d := range docs {
		idx.exactPaths[d.path] = d.id
		idx.pathByID[d.id] = d.path

		// Full path and the path with its .md extension stripped.
		addName(d.path, d.id)
		addName(strings.TrimSuffix(d.path, ".md"), d.id)

		// Every "/"-delimited suffix (the basename is the shortest), with and
		// without the .md extension, covering shortest-unique-path resolution.
		for i := 0; i < len(d.path); i++ {
			if d.path[i] == '/' {
				suffix := d.path[i+1:]
				addName(suffix, d.id)
				addName(strings.TrimSuffix(suffix, ".md"), d.id)
			}
		}

		if d.title != "" {
			idx.titles[d.title] = append(idx.titles[d.title], d.id)
		}
	}

	return idx
}

// uniqueDocID returns the single docID indexed under name, or false if the name
// is absent or ambiguous (maps to multiple docs).
func (idx *lookupIndex) uniqueDocID(name string) (string, bool) {
	set := idx.nameIndex[name]
	if len(set) != 1 {
		return "", false
	}
	for id := range set {
		return id, true
	}
	return "", false
}

// pathsForSet returns the sorted vault-relative paths for a docID set.
func (idx *lookupIndex) pathsForSet(set map[string]struct{}) []string {
	paths := make([]string, 0, len(set))
	for id := range set {
		paths = append(paths, idx.pathByID[id])
	}
	sort.Strings(paths)
	return paths
}

// pathsForIDs returns the sorted vault-relative paths for a docID slice.
func (idx *lookupIndex) pathsForIDs(ids []string) []string {
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		paths = append(paths, idx.pathByID[id])
	}
	sort.Strings(paths)
	return paths
}

// ResolveTarget resolves a user-supplied name to a single document's
// vault-relative path using the same tiered matching as wikilink resolution:
//
//	A. exact vault-relative path (with and without .md)
//	B. shortest-unique path suffix / basename
//	C. title
//	D. alias
//
// Unlike ResolveLinks (which silently leaves an ambiguous wikilink unresolved),
// ResolveTarget FAILS LOUDLY on ambiguity, returning *AmbiguousTargetError with
// the candidate paths so a caller never writes to a guessed file. A leading
// slash, backslashes, and any #heading / #^block anchor are stripped before
// matching. Returns ErrTargetNotFound when nothing matches.
func (db *DB) ResolveTarget(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	// Strip a trailing #heading or #^block anchor (resolution is by note).
	if i := strings.Index(name, "#"); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrTargetNotFound
	}

	docs, err := db.fetchDocInfos()
	if err != nil {
		return "", err
	}
	aliases, err := db.fetchAliases()
	if err != nil {
		return "", err
	}
	idx := buildLookupIndex(docs, aliases)

	// A. Exact full-path match (path is unique).
	if id, ok := idx.exactPaths[name]; ok {
		return idx.pathByID[id], nil
	}
	if id, ok := idx.exactPaths[name+".md"]; ok {
		return idx.pathByID[id], nil
	}

	// B. Shortest-unique-name match (path suffix or basename). A tier hit with
	// more than one document is an ambiguity error, not a fall-through.
	for _, key := range []string{name, name + ".md"} {
		set := idx.nameIndex[key]
		switch len(set) {
		case 0:
			continue
		case 1:
			for id := range set {
				return idx.pathByID[id], nil
			}
		default:
			return "", &AmbiguousTargetError{Name: name, Candidates: idx.pathsForSet(set)}
		}
	}

	// C. Title match.
	if ids := idx.titles[name]; len(ids) == 1 {
		return idx.pathByID[ids[0]], nil
	} else if len(ids) > 1 {
		return "", &AmbiguousTargetError{Name: name, Candidates: idx.pathsForIDs(ids)}
	}

	// D. Alias match.
	if ids := idx.aliases[name]; len(ids) == 1 {
		return idx.pathByID[ids[0]], nil
	} else if len(ids) > 1 {
		return "", &AmbiguousTargetError{Name: name, Candidates: idx.pathsForIDs(ids)}
	}

	return "", ErrTargetNotFound
}
