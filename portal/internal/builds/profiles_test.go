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
