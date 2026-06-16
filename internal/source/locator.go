package source

import (
	"encoding/base64"
	"strings"

	"github.com/sorafujitani/ccsession/internal/session"
)

func LocatorFor(s *session.Session) string {
	if s == nil {
		return ""
	}
	raw := s.JSONLPath
	if raw == "" {
		if _, local, ok := splitKey(s.ID); ok {
			raw = local
		} else {
			raw = s.ID
		}
	}
	locator := encodeLocator(raw)
	if name, _, ok := splitKey(s.ID); ok && s.Source == name {
		return joinKey(name, locator)
	}
	return locator
}

func encodeLocator(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeLocator(locator string) (string, bool) {
	if strings.TrimSpace(locator) == "" {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(locator)
	if err != nil {
		return "", false
	}
	return string(raw), true
}
