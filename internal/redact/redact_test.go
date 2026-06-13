package redact

import (
	"testing"
)

func TestRedactJWT(t *testing.T) {
	input := "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dGVzdHNpZ25hdHVyZQ"
	want := "Bearer ***"
	got := String(input)
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestRedactJWTInBody(t *testing.T) {
	input := `{"access_token":"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dGVzdA","refresh_token":"x.y.z"}`
	got := String(input)
	if containsToken(got) {
		t.Errorf("String() still contains JWT: %q", got)
	}
}

func TestRedactAPIError(t *testing.T) {
	input := `status=401 body={"error":"unauthorized","access_token":"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dGVzdA"}`
	got := String(input)
	if containsToken(got) {
		t.Errorf("String() still contains token in API error: %q", got)
	}
	if !contains(got, "***") {
		t.Errorf("String() should contain ***: %q", got)
	}
}

func TestRedactRefreshTokenField(t *testing.T) {
	input := `"refresh_token":"super-secret-refresh-token-value"`
	got := String(input)
	if containsToken(got) {
		t.Errorf("String() still contains refresh_token: %q", got)
	}
}

func TestRedactPlainText(t *testing.T) {
	input := "Hello, this is a normal message without tokens"
	got := String(input)
	if got != input {
		t.Errorf("String() should not modify plain text: got %q, want %q", got, input)
	}
}

func TestRedactShortHex(t *testing.T) {
	input := "abc123def456"
	got := String(input)
	if got != input {
		t.Errorf("String() should not modify short hex: got %q, want %q", got, input)
	}
}

func TestRedactReaderWithToken(t *testing.T) {
	input := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dGVzdHNpZ25hdHVyZQ"
	got := Reader(input)
	if containsToken(got) {
		t.Errorf("Reader() still contains JWT: %q", got)
	}
}

func TestRedactReaderWithoutToken(t *testing.T) {
	input := "normal text without any sensitive data"
	got := Reader(input)
	if got != input {
		t.Errorf("Reader() should not modify safe text: got %q, want %q", got, input)
	}
}

func containsToken(s string) bool {
	return jwtPattern.MatchString(s) ||
		refreshPattern.MatchString(s) ||
		accessPattern.MatchString(s)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
