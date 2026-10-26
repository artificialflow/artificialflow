package auth

import "testing"

func FuzzExtractBearerToken(f *testing.F) {
	for _, seed := range []string{
		"",
		"Bearer token",
		"bearer token-with-symbols._-",
		"Basic dXNlcjpwYXNz",
		"Bearer ",
		"Bearer one two",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, authorization string) {
		if len(authorization) > 16*1024 {
			t.Skip()
		}
		token := extractBearerToken(authorization)
		if token != "" && len(token) > len(authorization) {
			t.Fatalf("extracted token cannot exceed input length")
		}
	})
}
