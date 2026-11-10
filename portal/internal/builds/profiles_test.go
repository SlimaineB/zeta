package builds

import "testing"

func TestLoadDefaultProfiles(t *testing.T) {
	reg, err := LoadProfiles("")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.List()) < 2 {
		t.Fatalf("expected at least 2 profiles, got %d", len(reg.List()))
	}
	cpp, ok := reg.Get("cpp")
	if !ok {
		t.Fatal("cpp profile missing")
	}
	if cpp.WorkDir != "/workspace" {
		t.Fatalf("cpp workdir = %q", cpp.WorkDir)
	}
	if cpp.DefaultRepo == "" {
		t.Fatal("cpp default repo missing")
	}
	if len(cpp.Commands) == 0 {
		t.Fatal("cpp commands missing")
	}
	python, ok := reg.Get("python")
	if !ok {
		t.Fatal("python profile missing")
	}
	if python.ArtifactPath != "/artifacts/python" {
		t.Fatalf("python artifact path = %q", python.ArtifactPath)
	}
	goProfile, ok := reg.Get("go")
	if !ok {
		t.Fatal("go profile missing")
	}
	if goProfile.Image != "zeta/go:0.1" {
		t.Fatalf("go image = %q", goProfile.Image)
	}
	if goProfile.ArtifactPath != "/artifacts/go" {
		t.Fatalf("go artifact path = %q", goProfile.ArtifactPath)
	}
	if goProfile.Env["GOCACHE"] != "/cache/go-build" {
		t.Fatalf("go GOCACHE = %q", goProfile.Env["GOCACHE"])
	}
}

func TestApplyImageOverrides(t *testing.T) {
	reg, err := LoadProfiles("")
	if err != nil {
		t.Fatal(err)
	}
	reg.ApplyImageOverrides("ghcr.io/slimaineb", "1.2.3")
	goProfile, _ := reg.Get("go")
	if goProfile.Image != "ghcr.io/slimaineb/zeta/go:1.2.3" {
		t.Fatalf("go image = %q", goProfile.Image)
	}
	cpp, _ := reg.Get("cpp")
	if cpp.Image != "ghcr.io/slimaineb/zeta/cpp:1.2.3" {
		t.Fatalf("cpp image = %q", cpp.Image)
	}
}

func TestRewriteImage(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		registry string
		tag      string
		want     string
	}{
		{"local tag", "zeta/go:0.1", "", "", "zeta/go:0.1"},
		{"local tag override", "zeta/go:0.1", "", "2.0.0", "zeta/go:2.0.0"},
		{"registry only", "zeta/go:0.1", "ghcr.io/owner", "", "ghcr.io/owner/zeta/go:0.1"},
		{"registry and tag", "zeta/go:0.1", "ghcr.io/owner", "2.0.0", "ghcr.io/owner/zeta/go:2.0.0"},
		{"already qualified", "ghcr.io/other/go:1", "ghcr.io/owner", "2.0.0", "ghcr.io/other/go:2.0.0"},
		{"localhost", "localhost:5000/zeta/go:1", "ghcr.io/owner", "2.0.0", "localhost:5000/zeta/go:2.0.0"},
		{"digest", "zeta/go@sha256:abc", "ghcr.io/owner", "2.0.0", "ghcr.io/owner/zeta/go@sha256:abc"},
		{"no tag", "zeta/go", "ghcr.io/owner", "2.0.0", "ghcr.io/owner/zeta/go:2.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteImage(tc.image, tc.registry, tc.tag); got != tc.want {
				t.Fatalf("rewriteImage(%q, %q, %q) = %q, want %q", tc.image, tc.registry, tc.tag, got, tc.want)
			}
		})
	}
}
