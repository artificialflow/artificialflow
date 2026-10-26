package httpproblem

import "testing"

func FuzzIsVersionedPath(f *testing.F) {
	for _, seed := range []string{"/v1", "/v1/", "/v1/instances", "/v10", "/", "", "/v1\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if len(path) > 16*1024 {
			t.Skip()
		}
		got := isVersionedPath(path)
		want := path == "/v1" || (len(path) >= 4 && path[:4] == "/v1/")
		if got != want {
			t.Fatalf("isVersionedPath(%q)=%v, want %v", path, got, want)
		}
	})
}
