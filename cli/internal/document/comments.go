package document

// StripComments blanks Obsidian comments (%% ... %%, inline or spanning
// multiple lines) by replacing the comment bytes with spaces while preserving
// newlines. Preserving the newline count keeps chunk line numbers and byte
// offsets stable, so a comment never shifts the StartLine/EndLine of later
// chunks. The comment text is removed from the indexable representation
// (search, embeddings) only — the on-disk file is never modified.
//
// Limitation (v1): a "%%" appearing inside a code span/fence is still treated
// as a comment delimiter. That's rare and the worst case is a blanked code
// snippet in the index, not data loss.
func StripComments(body string) string {
	out := []byte(body)
	n := len(body)
	for i := 0; i < n; {
		if i+1 < n && body[i] == '%' && body[i+1] == '%' {
			// Find the closing "%%" after the opening one.
			j := i + 2
			for j+1 < n && !(body[j] == '%' && body[j+1] == '%') {
				j++
			}
			end := n // unterminated comment: blank to EOF
			if j+1 < n && body[j] == '%' && body[j+1] == '%' {
				end = j + 2 // include the closing "%%"
			}
			for k := i; k < end; k++ {
				if out[k] != '\n' {
					out[k] = ' '
				}
			}
			i = end
			continue
		}
		i++
	}
	return string(out)
}

// IndexableBody returns the document body as it should be indexed and embedded:
// with Obsidian comments stripped. Use this anywhere body text feeds search,
// chunking, or embeddings so comments never leak into the index.
func (d *Document) IndexableBody() string {
	return StripComments(d.Body)
}
