package document

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var sensitiveKeys = map[string]bool{
	"secret":   true,
	"password": true,
	"token":    true,
	"key":      true,
}

func IsSensitiveKey(key string) bool {
	return sensitiveKeys[strings.ToLower(key)]
}

// emptyFrontmatterBlock reports whether the region after the opening delimiter
// begins with the CLOSING delimiter, i.e. the note opens with an empty
// frontmatter block ("---\n---\n"), and returns the body that follows it.
//
// Obsidian writes that shape when every property is removed from a note, and
// SerializeFrontmatter writes it for an empty map, so 2nb produces it itself.
// Without this check the closing-delimiter search below runs straight past the
// empty block and matches the next "---" in the BODY (a horizontal rule),
// handing real prose to the YAML parser. The note then fails to parse on every
// index, and one such note was enough to fail a whole force-reembed and roll
// 313 good embeddings back.
func emptyFrontmatterBlock(rest string) (body string, ok bool) {
	// The SAME predicate the closing fence uses. Demanding an exact "---" here
	// while closingFence tolerated a trailing space is what made an empty block
	// with one read as unterminated: the note became all body, and the next
	// write prepended a fresh block in front of the original, leaving the note
	// with two frontmatter blocks and the old fence stranded in its text.
	n, ok := fenceLineLen(rest)
	if !ok {
		return "", false
	}
	return rest[n:], true
}

// isFrontmatterSpace reports whether a rune is invisible filler on an otherwise
// empty line. unicode.IsSpace covers space, tab, CR, formfeed, vertical tab,
// NBSP and the U+2028/U+2029 separators. The zero-width characters are FORMAT
// characters, not spaces, so IsSpace says no to them and they have to be named:
// each one is invisible in every editor, so a line carrying one looks blank to
// the person who left it there.
func isFrontmatterSpace(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	switch r {
	case '\u200b', // zero-width space
		'\ufeff', // zero-width no-break space / BOM
		'\u200c', // zero-width non-joiner
		'\u200d': // zero-width joiner
		return true
	}
	return false
}

// isBlankLine reports whether one line of a frontmatter region is blank. Blank
// means INVISIBLE, not byte-empty: a line carrying a stray space, tab or
// zero-width character is blank to the reader and to the editor that left it
// there. Narrower definitions cost data in both directions, because this
// predicate decides both whether prose is absorbed as properties and whether
// real properties survive. The CR of a CRLF line is handled here rather than by
// normalizing the document, so the body that comes back is the original bytes.
func isBlankLine(line string) bool {
	return strings.TrimFunc(line, isFrontmatterSpace) == ""
}

// contiguousKeyBlock returns the YAML text to parse from the region between a
// doubled opening
// fence and its closing fence is what frontmatter actually is: a CONTIGUOUS
// block of keys. Leading blank lines are skipped (a note may open its properties
// a line down), and after that the block must contain NO blank line at all,
// interior or trailing.
//
// That single test is what tells the two competing shapes apart, and nothing
// simpler does, because BOTH of them have a blank line right after the doubled
// fence:
//
//	"---\n---\n\nStatus: draft\n\n---\n\nRest\n"              body, blank line before the fence
//	"---\n---\n\ntitle: Real Note\ntags: [a, b]\n---\nBody\n"   properties, none
//
// Judging by the LEADING blank line alone got both wrong in turn: it absorbed
// the first (whenever that blank line carried whitespace) and discarded the
// second outright, moving a note's real title and tags into its body.
func contiguousKeyBlock(region string) (string, bool) {
	lines := strings.Split(region, "\n")
	first := 0
	for first < len(lines) && isBlankLine(lines[first]) {
		first++
	}
	if first == len(lines) {
		return "", false // nothing but blank lines is not a key block
	}
	for _, line := range lines[first:] {
		if isBlankLine(line) {
			return "", false
		}
	}
	// The recognized leading blanks are STRIPPED, not merely stepped over. They
	// used to be left in the region handed to the YAML parser, which is the root
	// cause of the worst version of this bug: YAML forbids tab indentation, so a
	// tab-only "blank" line the classifier had already accepted made the whole
	// region fail to parse and a note's real properties were discarded into its
	// body. Validating a region and then parsing a different one is the shape of
	// that mistake; there is one region now.
	return strings.Join(lines[first:], "\n"), true
}

// closingFence locates the fence that ENDS a frontmatter region and returns the
// index of the NEWLINE THAT PRECEDES it (the CR of a CRLF ending, so the YAML
// region never keeps a stray "\r") plus the number of bytes from there to the
// first byte of the body. It returns (-1, 0) when there is none.
//
// A fence is a LINE THAT IS EXACTLY "---". Anything else on that line makes it
// BODY, and getting that wrong destroyed notes: the search used to be
// strings.Index(rest, "\n---") with no check that the match was at end of file,
// so the FIRST "\n---" anywhere in the note ended the frontmatter and
// everything after it was discarded as if the file stopped there. A markdown
// horizontal rule ("----"), a longer one ("--------"), a fence carrying a
// trailing space, and a line beginning "---more" each cost a note its entire
// body, on read and then on disk, because Serialize rewrites the file from that
// truncated body. Verified against 0.22.3, so it shipped.
//
// Trailing horizontal whitespace IS accepted, and that is a deliberate choice
// rather than an oversight: a space or a tab at the end of the fence is
// invisible in every editor, exactly like the tab that made isBlankLine judge
// blankness by INVISIBILITY rather than emptiness. Rejecting it would not
// corrupt anything now (an unterminated block reads as a body-only note) but it
// would silently drop a note's properties over a character nobody can see. The
// body still starts after that whole line.
//
// It is the single definition of where frontmatter ends, called by the reader
// (parseFrontmatterFull), the doubled-fence reader, and the surgical writer.
// They used to search separately; the reader's own copy is what carried the
// missing end-of-file check while this function had it.
func closingFence(rest string) (idx, length int) {
	for i := 0; i < len(rest); i++ {
		if rest[i] != '\n' {
			continue
		}
		n, ok := fenceLineLen(rest[i+1:])
		if !ok {
			continue
		}
		// The CR of a CRLF ending belongs to the LINE ENDING, not to the YAML
		// region: slicing it in leaves a stray "\r" on the last property.
		start := i
		if i > 0 && rest[i-1] == '\r' {
			start = i - 1
		}
		return start, (i + 1 + n) - start
	}
	return -1, 0
}

// fenceLineLen reports whether s BEGINS with a fence line, and how many bytes
// that line occupies with its terminating newline included. A fence at end of
// file needs no newline.
//
// It is THE fence predicate. Every fence decision in this file goes through it:
// the opening fence, the closing fence, the empty-block check and the surgical
// writer's own opening check. Four of those used to test string prefixes
// themselves, and the disagreement cost data twice over. Teaching the CLOSING
// fence to tolerate a trailing space while emptyFrontmatterBlock still demanded
// an exact match made an empty block read as UNTERMINATED, so the whole note
// became body and a write prepended a second frontmatter block in front of the
// first. And an opening fence carrying the same invisible character was not
// recognized at all, so every property fell into the body. Two definitions of
// one boundary is the shape this file has now been fixed for three times.
//
// Invisible filler after the "---" is accepted, and INVISIBLE means what
// isBlankLine means by it: unicode.IsSpace plus the zero-width format
// characters, minus the line terminators, which end the line rather than pad
// it. isFrontmatterSpace is shared with isBlankLine deliberately: a fence and a
// blank line disagreeing about which characters are invisible is exactly how
// the tab bug came back in cycle 3.
func fenceLineLen(s string) (int, bool) {
	if !strings.HasPrefix(s, "---") {
		return 0, false
	}
	for i := 3; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\n':
			return i + size, true
		case r == '\r':
			// The CR of a CRLF ending, or a lone CR at end of file.
			if i+size < len(s) && s[i+size] == '\n' {
				return i + size + 1, true
			}
			if i+size == len(s) {
				return i + size, true
			}
			return 0, false
		case isFrontmatterHorizontalSpace(r):
			i += size
		default:
			return 0, false
		}
	}
	return len(s), true
}

// isFrontmatterHorizontalSpace reports whether r is invisible filler WITHIN a
// line: everything isFrontmatterSpace calls invisible, minus the terminators
// that end a line instead of padding it.
func isFrontmatterHorizontalSpace(r rune) bool {
	switch r {
	case '\n', '\r', '\u2028', '\u2029':
		return false
	}
	return isFrontmatterSpace(r)
}

// openingFenceLen reports the length of the note's OPENING fence, or (0, false)
// when the note does not open with one. Same predicate as the closing fence, so
// a trailing space is tolerated at both ends or at neither.
func openingFenceLen(s string) (int, bool) {
	return fenceLineLen(s)
}

// legacyDoubledDelimiterFrontmatter re-reads a doubled opening delimiter the way
// the parser did before the empty-block rule: everything up to the NEXT closing
// delimiter is the YAML region, and its leading "---" is a document start marker
// that YAML accepts. It wins only when that region yields a NON-EMPTY mapping;
// a parse error or an empty result means the block really is empty.
//
// The region between the two fences is real frontmatter only when it is a
// contiguous key block (contiguousKeyBlock) AND parses as a non-empty mapping.
// Anything else means the block really is empty and the whole region is body.
// The closing fence is found exactly as the main parser finds one: "\n---\n",
// its CRLF form, or a "---" at end of file.
//
// Both halves of that test cost real data when they were missing. Reading the
// region as properties whenever it parsed absorbed body prose, and because
// UpdateDocumentFrontmatterAST consults this same function, `2nb meta --set` and
// `2nb tag add` then REWROTE the file, lifting those lines out of the visible
// body and into a properties block on disk. Rejecting the region whenever a
// blank line followed the fence did the mirror image: a note whose real title
// and tags began one line down had them discarded into its body, and rewritten
// there. A frontmatter-only command must never touch the body, in either
// direction.
//
// What is left is genuinely ambiguous and deliberately kept: a body whose first
// non-blank stretch has no blank line in it and happens to parse as a mapping
// ("---\n---\nStatus: draft\n---\nbody") is read as properties. Prose that runs
// to more than one paragraph, or that ends a paragraph before the next "---",
// is not, which covers the shapes notes actually take. Losing real metadata is
// the worse of the two failures, so that is the side the tie falls on, and it is
// pinned by test rather than left accidental.
func doubledFenceKeyBlock(rest string) (block, body string, ok bool) {
	afterOpen, ok := fenceLineLen(rest)
	if !ok || afterOpen >= len(rest) {
		// A second delimiter at end of file: an empty block, nothing behind it.
		return "", "", false
	}
	idx, closeLen := closingFence(rest)
	// One guard for two cases: no closing fence at all (idx == -1), and a
	// TRIPLED delimiter, where the fence found is the one already consumed and
	// there is no region between them (slicing it would panic).
	if idx < afterOpen {
		return "", "", false
	}

	// The ORIGINAL bytes, sliced. Nothing here normalizes line endings: the body
	// returned is what was read, so a CRLF note keeps its CRLF through the write
	// path that rewrites the whole file from it.
	block, ok = contiguousKeyBlock(rest[afterOpen:idx])
	if !ok {
		return "", "", false
	}
	return block, rest[idx+closeLen:], true
}

// legacyDoubledDelimiterFrontmatter parses the region doubledFenceKeyBlock
// isolated. The split exists so the surgical writer can parse the SAME text: it
// used to derive its own region, and one that merely skipped the leading blank
// lines rather than stripping them handed YAML filler the reader had already
// removed, which threw a note's properties away on a `meta --set`.
func legacyDoubledDelimiterFrontmatter(rest string) (map[string]any, rawFrontmatter, string, bool) {
	block, body, ok := doubledFenceKeyBlock(rest)
	if !ok {
		return nil, nil, "", false
	}
	meta := make(map[string]any)
	if err := yaml.Unmarshal([]byte(block), &meta); err != nil || len(meta) == 0 {
		return nil, nil, "", false
	}
	return meta, rawFrontmatterOf(block), body, true
}

// ParseFrontmatter returns the RESOLVED frontmatter map and the body. Use
// parseFrontmatterFull when the ORIGINAL text of a value matters (see
// frontmatter_scalar.go); this wrapper exists because most callers only read
// or rewrite the map.
func ParseFrontmatter(content []byte) (meta map[string]any, body string, err error) {
	meta, _, body, err = parseFrontmatterFull(content)
	return meta, body, err
}

// parseFrontmatterFull is ParseFrontmatter plus the verbatim text of every
// top-level key, read from the SAME region the map was decoded from. Deriving
// the region twice is the shape of a bug this file has been fixed for twice
// already, so there is exactly one boundary search and both readings come out
// of it.
func parseFrontmatterFull(content []byte) (meta map[string]any, raw rawFrontmatter, body string, err error) {
	s := string(content)

	// Length of the opening delimiter depends on which line ending the file
	// uses. Skipping a fixed 4 bytes for a "---\r\n" opening leaves a stray
	// "\n" at the start of the YAML region — harmless today (YAML tolerates
	// leading whitespace) but throws off every offset downstream.
	openLen, ok := openingFenceLen(s)
	if !ok {
		return nil, nil, s, nil
	}

	rest := s[openLen:]
	if emptyBody, ok := emptyFrontmatterBlock(rest); ok {
		// A doubled delimiter is genuinely ambiguous, so prefer the reading that
		// cannot LOSE data. "---\n---\nreal: value\n---\nbody" is either an
		// empty block followed by prose that happens to look like YAML, or a
		// mapping whose region opens with a stray document marker. Before the
		// empty-block rule existed the second reading always won (the search
		// below found the THIRD delimiter and YAML accepted the leading "---" as
		// a document start), so taking the first reading unconditionally moved a
		// note's real metadata into its body.
		if meta, raw, legacyBody, ok := legacyDoubledDelimiterFrontmatter(rest); ok {
			return meta, raw, legacyBody, nil
		}
		return map[string]any{}, nil, emptyBody, nil
	}
	// ONE boundary search, the same function the doubled-fence reader and the
	// surgical writer call. This used to be an inline chain of strings.Index
	// calls that duplicated closingFence and disagreed with it, which is the
	// shape two earlier bugs in this file took; the copy is what carried the
	// missing end-of-file check that truncated bodies.
	idx, closeLen := closingFence(rest)
	if idx == -1 {
		// An UNTERMINATED block is not frontmatter. Returning the whole file as
		// body is the reading that cannot lose anything: the note simply has no
		// properties, which is what it literally says.
		return nil, nil, s, nil
	}
	meta, raw, err = decodeFrontmatterYAML(rest[:idx])
	if err != nil {
		return nil, nil, s, err
	}
	return meta, raw, rest[idx+closeLen:], nil
}

// decodeFrontmatterYAML decodes ONE frontmatter region both ways: the resolved
// map every caller reads, and the verbatim text of its top-level values. Both
// come from the same string, so they cannot describe different regions.
func decodeFrontmatterYAML(yamlStr string) (map[string]any, rawFrontmatter, error) {
	meta := make(map[string]any)
	if err := yaml.Unmarshal([]byte(yamlStr), &meta); err != nil {
		return nil, nil, fmt.Errorf("malformed YAML frontmatter: %w", err)
	}
	return meta, rawFrontmatterOf(yamlStr), nil
}

func SerializeFrontmatter(meta map[string]any) ([]byte, error) {
	if len(meta) == 0 {
		return []byte("---\n---\n"), nil
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(meta); err != nil {
		return nil, fmt.Errorf("serialize frontmatter: %w", err)
	}
	enc.Close()
	buf.WriteString("---\n")
	return buf.Bytes(), nil
}

func SerializeDocument(meta map[string]any, body string) ([]byte, error) {
	fm, err := SerializeFrontmatter(meta)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(fm)
	if body != "" {
		// An EMPTY frontmatter block is closed by a delimiter identical to the
		// one that opened it, so what the parser meets is a doubled fence, and
		// the blank line after it is how it knows the block really is empty
		// (see legacyDoubledDelimiterFrontmatter). Write no blank line and 2nb
		// produces a note it then MISREADS: an empty map with the body
		// "Status: draft\n\n---\n\nAction items\n" was written as
		// "---\n---\nStatus: draft\n\n---\n\nAction items\n" and read straight
		// back as {Status: draft} with that line gone from the body. Exactly one
		// newline, and only when the body does not already begin with one, so a
		// body that is already spaced is emitted byte for byte and no blank line
		// is ever doubled. A non-empty block needs nothing: its closing
		// delimiter is unambiguous. SerializeFrontmatter's own return is
		// deliberately unchanged, since it promises the block and not the note.
		if len(meta) == 0 && !strings.HasPrefix(body, "\n") && !strings.HasPrefix(body, "\r\n") {
			buf.WriteString("\n")
		}
		buf.WriteString(body)
	}
	return buf.Bytes(), nil
}

// FilterSensitive returns a copy of meta with sensitive keys removed.
func FilterSensitive(meta map[string]any) map[string]any {
	filtered := make(map[string]any, len(meta))
	for k, v := range meta {
		if !IsSensitiveKey(k) {
			filtered[k] = v
		}
	}
	return filtered
}

// materializeAliasesInto rewrites every alias that points ANYWHERE into the
// subtree rooted at doomed, replacing it with a copy of the value it names, so
// that removing or replacing that subtree cannot leave a dangling alias.
//
// This is the surgical writer's hardest case and it produced an INVALID note.
// `status: &s draft` with `other: *s` edited to status=published wrote
// `status: published` while leaving `other: *s`, and the anchor that defined
// `s` was gone: the file no longer parses, so the note fails every future read,
// drops out of the index, and `meta`/`tag` on it error out. Worse than the
// wholesale re-marshal it replaced, which resolved the alias and stayed valid.
//
// RESOLVING the alias is the deliberate choice over carrying the anchor onto
// the replacement. Carrying it would make `other` follow `status` to
// "published", silently changing the value of a key the user did not touch,
// which is the very thing the surgical write exists to prevent. Resolving keeps
// `other: draft`, its VALUE, and loses only the anchor structure. That also
// matches what the released build did.
//
// An alias pointing at a node NESTED inside doomed counts, which is why the
// whole subtree is collected rather than just the node itself. Editing the
// ALIAS rather than the anchor needs nothing: an anchor with no remaining
// reference is valid YAML.
func materializeAliasesInto(root, doomed *yaml.Node) {
	if root == nil || doomed == nil {
		return
	}
	doomedNodes := map[*yaml.Node]bool{}
	var collect func(*yaml.Node)
	collect = func(n *yaml.Node) {
		if n == nil || doomedNodes[n] {
			return
		}
		doomedNodes[n] = true
		for _, c := range n.Content {
			collect(c)
		}
	}
	collect(doomed)

	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		for i, c := range n.Content {
			if c == nil {
				continue
			}
			if c.Kind == yaml.AliasNode && doomedNodes[c.Alias] {
				n.Content[i] = cloneNode(c.Alias)
				continue
			}
			walk(c)
		}
	}
	walk(root)
}

// cloneNode deep-copies a node, dropping the anchor so a materialized copy
// never redefines a name whose definition is on its way out.
func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Anchor = ""
	c.Alias = nil
	if n.Content != nil {
		c.Content = make([]*yaml.Node, len(n.Content))
		for i, child := range n.Content {
			c.Content[i] = cloneNode(child)
		}
	}
	return &c
}

// valueNode marshals one frontmatter value into the AST node that represents it.
func valueNode(v any) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		return nil, err
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0], nil
	}
	return &n, nil
}

// nodeHoldsValue reports whether an existing AST node already carries value, so
// the writer can leave that node exactly as the file wrote it.
//
// It decodes the node and compares, rather than comparing rendered text,
// because the question is whether the VALUE changed: `modified: 2020-01-01` and
// the time.Time it decodes to are the same value, and rewriting the node would
// spell it `2020-01-01T00:00:00Z`. The marshal comparison is the fallback
// because a time.Time carrying a zone OFFSET decodes to a fresh *time.Location
// every time, so reflect.DeepEqual says no to two readings of one instant. A
// node that will not decode is treated as changed, which is the safe direction:
// it gets rewritten from the value the caller holds.
func nodeHoldsValue(node *yaml.Node, value any) bool {
	var current any
	if err := node.Decode(&current); err != nil {
		return false
	}
	if reflect.DeepEqual(current, value) {
		return true
	}
	a, aerr := yaml.Marshal(current)
	b, berr := yaml.Marshal(value)
	return aerr == nil && berr == nil && bytes.Equal(a, b)
}

// UpdateDocumentFrontmatterAST updates the frontmatter of a document surgically,
// preserving comments, formatting, and key order for all untouched fields.
func UpdateDocumentFrontmatterAST(original []byte, updatedMeta map[string]any, body string) ([]byte, error) {
	s := string(original)
	openLen, ok := openingFenceLen(s)
	if !ok {
		return SerializeDocument(updatedMeta, body)
	}

	rest := s[openLen:]
	// The SAME boundary the reader used, not a second search for it.
	idx, _ := closingFence(rest)
	if idx == -1 {
		return SerializeDocument(updatedMeta, body)
	}
	yamlStr := rest[:idx]

	// An empty frontmatter block carries no YAML to preserve surgically, and the
	// closing-fence search would otherwise match a "---" in the body and treat
	// body prose as the region to edit. A doubled delimiter that still reads as
	// real frontmatter DOES have a region to preserve, and it is the reader's
	// region, byte for byte. Re-deriving it here is how the writer ended up
	// handing YAML the blank filler the reader had already stripped: a tab-only
	// line is legal filler and illegal indentation, so the parse failed and a
	// `meta --set` threw the note's properties away.
	if _, empty := emptyFrontmatterBlock(rest); empty {
		block, _, isBlock := doubledFenceKeyBlock(rest)
		if !isBlock {
			return SerializeDocument(updatedMeta, body)
		}
		yamlStr = block
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(yamlStr), &node); err != nil {
		return nil, err
	}

	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return nil, fmt.Errorf("invalid YAML document node")
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("frontmatter must be a MappingNode")
	}

	// 1. Update CHANGED keys, insert new ones, and leave every untouched key's
	// node exactly as the file wrote it.
	//
	// This used to replace the value node of EVERY key with a freshly marshaled
	// one, which made a frontmatter-only command rewrite properties nobody
	// touched: an unrelated `meta --set status=published` turned
	// `modified: 2020-01-01` into `2020-01-01T00:00:00Z`, `title: 2026-09-04`
	// into a timestamp, `id: 007` into `7`, `num: 3.50` into `3.5`, a flow list
	// into block style, and it dropped a value-attached comment. Once the READ
	// side learned to preserve a note's own text, that left the two disagreeing:
	// `list` showed `2026-09-04` while the file on disk became
	// `2026-09-04T00:00:00Z` the moment any other property was edited.
	//
	// nodeHoldsValue is the test, and it compares VALUES rather than text, so a
	// key is rewritten exactly when its value actually changed.
	//
	// Sorted, so a run that adds several keys appends them in a stable order
	// rather than whatever the map hands back.
	keys := make([]string, 0, len(updatedMeta))
	for k := range updatedMeta {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := updatedMeta[k]
		found := false
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value != k {
				continue
			}
			found = true
			if nodeHoldsValue(root.Content[i+1], v) {
				// Byte-identical: its scalar text, its style, its quoting and
				// any comment attached to it all survive untouched.
				break
			}
			vNode, err := valueNode(v)
			if err != nil {
				return nil, err
			}
			// Any alias pointing into the node about to go must be resolved
			// FIRST, using that node's current value, or the file stops
			// parsing.
			materializeAliasesInto(root, root.Content[i+1])
			// A comment sitting on the OLD value described the old value, so it
			// is deliberately not carried onto the new one. A comment attached
			// to the KEY is on the key node, which is never replaced.
			root.Content[i+1] = vNode
			break
		}
		if !found {
			vNode, err := valueNode(v)
			if err != nil {
				return nil, err
			}
			keyNode := &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: k,
			}
			root.Content = append(root.Content, keyNode, vNode)
		}
	}

	// 2. Remove keys that are not in updatedMeta
	var newContent []*yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if _, exists := updatedMeta[keyNode.Value]; exists {
			newContent = append(newContent, root.Content[i], root.Content[i+1])
			continue
		}
		// Removing a key that carried an anchor leaves the same dangling alias
		// a replacement would.
		materializeAliasesInto(root, root.Content[i+1])
	}
	root.Content = newContent

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return nil, err
	}
	enc.Close()

	// The note's OWN line endings. The yaml encoder emits LF, so a CRLF note
	// came back with LF fences and LF between its keys while its body kept
	// CRLF: one file, two conventions, from an edit that touched one property.
	// The opening fence is the note's own statement of which it uses.
	newline := "\n"
	if strings.HasSuffix(s[:openLen], "\r\n") {
		newline = "\r\n"
	}

	var docBuf bytes.Buffer
	docBuf.WriteString("---")
	docBuf.WriteString(newline)
	docBuf.WriteString(withNewline(buf.String(), newline))
	docBuf.WriteString("---")
	docBuf.WriteString(newline)
	docBuf.WriteString(body)

	return docBuf.Bytes(), nil
}

// withNewline re-emits LF-separated text with the note's line ending. The
// encoder always writes LF, so the text is normalized first and a CRLF note
// cannot end up with doubled carriage returns.
func withNewline(text, newline string) string {
	if newline == "\n" {
		return text
	}
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", newline)
}
