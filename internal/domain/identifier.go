package domain

// ValidAnimeID accepts a bounded ASCII slug as returned by the catalog.
// Disallowing dots, slashes, percent escapes, and URL syntax prevents traversal.
func ValidAnimeID(id string) bool {
	if len(id) == 0 || len(id) > 300 || id[0] == '-' || id[len(id)-1] == '-' {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func ValidEpisodeID(id string) bool {
	if len(id) == 0 || len(id) > 18 || id[0] == '0' {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
