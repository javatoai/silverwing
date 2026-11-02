package parser

import "strings"

// EscapeLinkDestination encodes a literal destination for Markdown's angle
// destination syntax. Markdown unescapes entities and backslashes once, even
// inside angle brackets; URL encoding alone does not protect literal &copy;.
func EscapeLinkDestination(destination string) string {
	return strings.NewReplacer("&", "&amp;", "\\", "\\\\", "<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(destination)
}
