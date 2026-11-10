package gitutil

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:org/repo.git": "ssh://git@github.com/org/repo.git",
		"git://git-server/cpp.git":    "git://git-server/cpp.git",
		"https://example.com/repo":    "https://example.com/repo",
		"ssh://git@example.com/repo":  "ssh://git@example.com/repo",
	}
	for in, want := range cases {
		got, err := NormalizeURL(in)
		if err != nil {
			t.Fatalf("NormalizeURL(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := NormalizeURL("file:///tmp/repo"); err == nil {
		t.Error("file:// should be rejected")
	}
}
