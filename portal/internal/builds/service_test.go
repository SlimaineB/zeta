package builds

import (
	"context"
	"io"
	"testing"

	"zeta/portal/internal/domain"
)

func TestValidRepoURL(t *testing.T) {
	valid := []string{
		"git://git-server/cpp.git",
		"https://example.com/org/repo.git",
		"ssh://git@example.com/org/repo.git",
		"http://example.com/repo",
	}
	for _, raw := range valid {
		if !validRepoURL(raw) {
			t.Errorf("validRepoURL(%q) = false, want true", raw)
		}
	}
	invalid := []string{
		"",
		"cpp.git",
		"/tmp/repo",
		"file:///tmp/repo",
		"git://",
		"https://",
		"https://example.com/repo with space",
	}
	for _, raw := range invalid {
		if validRepoURL(raw) {
			t.Errorf("validRepoURL(%q) = true, want false", raw)
		}
	}
}

func TestCommitPattern(t *testing.T) {
	valid := []string{"main", "v1.2.3", "3a4a9a8c572d0004f831900f7917a803aa4680fd", "feature/foo"}
	for _, ref := range valid {
		if !commitPattern.MatchString(ref) || containsDotDot(ref) {
			t.Errorf("commit %q should be valid", ref)
		}
	}
	invalid := []string{"", "-x", "foo..bar", "foo bar", "foo;bar", "$(id)"}
	for _, ref := range invalid {
		if commitPattern.MatchString(ref) && !containsDotDot(ref) {
			t.Errorf("commit %q should be invalid", ref)
		}
	}
}

func containsDotDot(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}

type fakeRepo struct {
	builds  []domain.Build
	created int
}

func (f *fakeRepo) Create(context.Context, domain.BuildSpec) (string, error) {
	f.created++
	return "zeta-test", nil
}
func (f *fakeRepo) StoreContext(context.Context, io.Reader) (string, error) { return "", nil }
func (f *fakeRepo) DeleteContext(context.Context, string) error             { return nil }
func (f *fakeRepo) List(context.Context) ([]domain.Build, error)            { return f.builds, nil }
func (f *fakeRepo) BuildOwner(context.Context, string) (string, error)      { return "", nil }
func (f *fakeRepo) StreamLogs(context.Context, string, io.Writer) error     { return nil }
func (f *fakeRepo) ReadLogs(context.Context, string, int64) (string, error) { return "", nil }
func (f *fakeRepo) StreamArtifact(context.Context, string, io.Writer) error { return nil }
func (f *fakeRepo) ListVolumes(context.Context) ([]domain.Volume, error)    { return nil, nil }
func (f *fakeRepo) VolumeQuota(context.Context) (domain.VolumeQuota, error) {
	return domain.VolumeQuota{}, nil
}
func (f *fakeRepo) ResizeVolume(context.Context, string, string) error { return nil }
func (f *fakeRepo) DeleteVolume(context.Context, string) error         { return nil }
func (f *fakeRepo) EnsureEnvironment(context.Context, domain.EnvSpec) (domain.Environment, error) {
	return domain.Environment{}, nil
}
func (f *fakeRepo) ListEnvironments(context.Context) ([]domain.Environment, error) { return nil, nil }
func (f *fakeRepo) SetEnvironmentActive(context.Context, string, bool) error       { return nil }
func (f *fakeRepo) DeleteEnvironment(context.Context, string) error                { return nil }
func (f *fakeRepo) EnvironmentOwner(context.Context, string) (string, error)       { return "", nil }
func (f *fakeRepo) EnvironmentToken(context.Context, string) (string, error)       { return "", nil }
func (f *fakeRepo) ExecEnvironment(context.Context, string, domain.ExecOptions) error {
	return nil
}

func TestEnsureCapacityDefault(t *testing.T) {
	repo := &fakeRepo{builds: []domain.Build{{Profile: "go", Status: domain.StatusRunning}}}
	svc := NewService(repo, nil)
	if err := svc.ensureCapacity(context.Background(), domain.Profile{Name: "go"}); err == nil {
		t.Fatal("expected default concurrency limit error")
	}
	if err := svc.ensureCapacity(context.Background(), domain.Profile{Name: "go", MaxConcurrent: 2}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ensureCapacity(context.Background(), domain.Profile{Name: "cpp"}); err != nil {
		t.Fatal(err)
	}
}
