package runner

import "strings"

func unitNameOK(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '@' || r == '.' || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

func unitContentOK(s string) bool {
	return !strings.ContainsAny(s, "\n\r") && !strings.ContainsRune(s, 0)
}
