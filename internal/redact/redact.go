package redact

import (
	"regexp"
	"strings"
)

var (
	jwtPattern     = regexp.MustCompile(`eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`)
	bearerPattern  = regexp.MustCompile(`(?i)bearer\s+eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`)
	refreshPattern = regexp.MustCompile(`"refresh_token"\s*:\s*"[^"]{8,}"`)
	accessPattern  = regexp.MustCompile(`"access_token"\s*:\s*"[^"]{8,}"`)
)

const tokenRedacted = "***"

func String(s string) string {
	if !strings.Contains(s, "eyJ") && !strings.Contains(s, "bearer") && !strings.Contains(s, "Bearer") && !strings.Contains(s, "refresh_token") && !strings.Contains(s, "access_token") {
		return s
	}
	s = bearerPattern.ReplaceAllString(s, "Bearer "+tokenRedacted)
	s = jwtPattern.ReplaceAllString(s, tokenRedacted)
	s = refreshPattern.ReplaceAllString(s, `"refresh_token":"`+tokenRedacted+`"`)
	s = accessPattern.ReplaceAllString(s, `"access_token":"`+tokenRedacted+`"`)
	return s
}
