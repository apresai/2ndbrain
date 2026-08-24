package document

import (
	"net/url"
	"strings"
)

// DecodeLinkTarget returns the percent-decoded form of a markdown-link path
// part, or the input unchanged when decoding fails (a literal '%' in a
// filename, e.g. "100%.md", must keep matching in its raw form). Obsidian
// percent-encodes spaces when it generates markdown links, so comparison
// sites decode the authored target before giving up on a raw match.
//
// Call it only AFTER the #anchor / ?query suffix split: decoding first would
// turn a literal %23 in a filename into '#' and mis-split the target.
//
// This helper is a member of the no-drift normalization set alongside
// normalizeTarget (rewrite.go), store.normalizeRawName, and the inline
// normalization in store.ResolveLinks: every site that compares an authored
// markdown target against a real path retries through this one function.
func DecodeLinkTarget(pathPart string) string {
	if !strings.Contains(pathPart, "%") {
		return pathPart
	}
	decoded, err := url.PathUnescape(pathPart)
	if err != nil {
		return pathPart
	}
	return decoded
}

// mdLinkEscapeSet is the exact byte set EncodeLinkTarget escapes: the bytes
// that break a bare CommonMark link destination or 2nb's own link parsing.
// '%' is included for round-trip fidelity (a literal-'%' filename must encode
// to a form that decodes back to itself); '/' is deliberately absent (path
// separators stay verbatim, which is why url.PathEscape cannot be used).
const mdLinkEscapeSet = "% #?()"

// EncodeLinkTarget percent-encodes exactly the bytes in mdLinkEscapeSet as
// uppercase %XX, leaving '/' and every other byte (including non-ASCII UTF-8)
// verbatim. DecodeLinkTarget(EncodeLinkTarget(s)) == s for every s.
func EncodeLinkTarget(path string) string {
	if !strings.ContainsAny(path, mdLinkEscapeSet) {
		return path
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(path) + 8)
	for i := 0; i < len(path); i++ {
		c := path[i]
		if strings.IndexByte(mdLinkEscapeSet, c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xF])
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// mdLinkNeedsEncoding reports whether a path emitted as a bare markdown link
// destination requires percent-encoding to stay a valid, parseable link.
func mdLinkNeedsEncoding(path string) bool {
	return strings.ContainsAny(path, mdLinkEscapeSet)
}
