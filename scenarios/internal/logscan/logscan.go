package logscan

import "strings"

// Fields parses "k=v" pairs after each occurrence of prefix, in order.
// Tokens without '=' are ignored. For duplicate keys on the same line,
// the last value wins.
func Fields(logs, prefix string) []map[string]string {
	var results []map[string]string
	for _, rawLine := range strings.Split(logs, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		at := strings.Index(line, prefix)
		if at < 0 {
			continue
		}
		rest := line[at+len(prefix):]
		m := make(map[string]string)
		for _, token := range strings.Fields(rest) {
			key, value, ok := strings.Cut(token, "=")
			if ok {
				m[key] = value
			}
		}
		results = append(results, m)
	}
	return results
}

// After returns the whitespace-delimited token following each occurrence of marker.
// If the marker appears at the end of the line with no token following, an empty
// string is returned for that occurrence.
func After(logs, marker string) []string {
	var results []string
	for _, rawLine := range strings.Split(logs, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		at := strings.Index(line, marker)
		if at < 0 {
			continue
		}
		rest := line[at+len(marker):]
		tokens := strings.Fields(rest)
		if len(tokens) > 0 {
			results = append(results, tokens[0])
		} else {
			results = append(results, "")
		}
	}
	return results
}
