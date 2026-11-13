package builds

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"zeta/portal/internal/domain"
)

var commitPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

type Limits struct {
	MaxBuildsPerUser int
	MaxEnvsPerUser   int
}

// LimitError reports that a profile or per-user quota was exceeded.
type LimitError struct {
	Message string
}

func (e *LimitError) Error() string { return e.Message }

type Service struct {
	repo     domain.BuildRepository
	registry *Registry
	limits   Limits
	mu       sync.Mutex
}

func NewService(repo domain.BuildRepository, registry *Registry, limits ...Limits) *Service {
	var l Limits
	if len(limits) > 0 {
		l = limits[0]
	}
	return &Service{repo: repo, registry: registry, limits: l}
}

func (s *Service) Profiles() []domain.Profile {
	return s.registry.List()
}

func (s *Service) Submit(ctx context.Context, req domain.SubmitRequest, owner string) (string, error) {
	profile, ok := s.registry.Get(req.Profile)
	if !ok {
		return "", fmt.Errorf("unknown profile: %s", req.Profile)
	}
	repo := strings.TrimSpace(req.Repo)
	if repo == "" {
		repo = profile.DefaultRepo
	}
	if repo == "" {
		return "", fmt.Errorf("Git repository required for profile %s", profile.Name)
	}
	if !validRepoURL(repo) {
		return "", fmt.Errorf("invalid repository URL: %s", repo)
	}
	commit := strings.TrimSpace(req.Commit)
	if commit != "" && (!commitPattern.MatchString(commit) || strings.Contains(commit, "..")) {
		return "", fmt.Errorf("invalid Git reference: %s", commit)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureCapacity(ctx, profile); err != nil {
		return "", err
	}
	if err := s.ensureUserBuildCapacity(ctx, owner); err != nil {
		return "", err
	}
	return s.repo.Create(ctx, domain.BuildSpec{
		Profile: profile,
		Repo:    repo,
		Commit:  commit,
		Owner:   owner,
	})
}

func (s *Service) SubmitContext(ctx context.Context, req domain.SubmitRequest, owner, contextPath, syncToken string) (string, error) {
	profile, ok := s.registry.Get(req.Profile)
	if !ok {
		return "", fmt.Errorf("unknown profile: %s", req.Profile)
	}
	if strings.TrimSpace(contextPath) == "" {
		return "", fmt.Errorf("empty context")
	}
	repo := strings.TrimSpace(req.Repo)
	if repo == "" {
		repo = "local context"
	}
	commit := strings.TrimSpace(req.Commit)
	if len(repo) > 512 || len(commit) > 256 || strings.ContainsAny(repo+commit, "\r\n") {
		return "", fmt.Errorf("invalid build metadata")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureCapacity(ctx, profile); err != nil {
		_ = s.repo.DeleteContext(ctx, contextPath)
		return "", err
	}
	if err := s.ensureUserBuildCapacity(ctx, owner); err != nil {
		_ = s.repo.DeleteContext(ctx, contextPath)
		return "", err
	}
	id, err := s.repo.Create(ctx, domain.BuildSpec{
		Profile:     profile,
		Repo:        repo,
		Commit:      commit,
		Owner:       owner,
		ContextPath: contextPath,
		SyncToken:   syncToken,
	})
	if err != nil {
		_ = s.repo.DeleteContext(ctx, contextPath)
		return "", err
	}
	return id, nil
}

func (s *Service) ensureCapacity(ctx context.Context, profile domain.Profile) error {
	limit := profile.MaxConcurrent
	if limit <= 0 {
		limit = 1
	}
	builds, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	active := 0
	for _, b := range builds {
		if b.Profile == profile.Name && (b.Status == domain.StatusPending || b.Status == domain.StatusRunning) {
			active++
		}
	}
	if active >= limit {
		return &LimitError{Message: fmt.Sprintf("concurrent build limit reached for profile %s (%d/%d)", profile.Name, active, limit)}
	}
	return nil
}

func (s *Service) ensureUserBuildCapacity(ctx context.Context, owner string) error {
	limit := s.limits.MaxBuildsPerUser
	owner = strings.TrimSpace(owner)
	if limit <= 0 || owner == "" {
		return nil
	}
	builds, err := s.repo.List(ctx)
	if err != nil {
		return err
	}
	active := 0
	for _, b := range builds {
		if b.Status != domain.StatusPending && b.Status != domain.StatusRunning {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(b.Owner), owner) {
			active++
		}
	}
	if active >= limit {
		return &LimitError{Message: fmt.Sprintf("you already have %d build(s) in progress (limit %d); wait for one to finish or check `zeta ls`", active, limit)}
	}
	return nil
}

func (s *Service) StoreContext(ctx context.Context, r io.Reader) (string, error) {
	return s.repo.StoreContext(ctx, r)
}

func (s *Service) DeleteContext(ctx context.Context, path string) error {
	return s.repo.DeleteContext(ctx, path)
}

func validRepoURL(raw string) bool {
	if strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "git", "http", "https", "ssh":
		return true
	default:
		return false
	}
}

func (s *Service) List(ctx context.Context) ([]domain.Build, error) {
	return s.repo.List(ctx)
}

func (s *Service) BuildOwner(ctx context.Context, id string) (string, error) {
	return s.repo.BuildOwner(ctx, id)
}

func (s *Service) StreamLogs(ctx context.Context, id string, w io.Writer) error {
	return s.repo.StreamLogs(ctx, id, w)
}

func (s *Service) Metrics(ctx context.Context, id string) (domain.CacheStats, error) {
	logs, err := s.repo.ReadLogs(ctx, id, 4000)
	if err != nil {
		return domain.CacheStats{}, err
	}
	return ParseMetrics(logs), nil
}

func (s *Service) Artifact(ctx context.Context, id string, w io.Writer) error {
	return s.repo.StreamArtifact(ctx, id, w)
}

func (s *Service) Volumes(ctx context.Context) (domain.VolumeList, error) {
	volumes, err := s.repo.ListVolumes(ctx)
	if err != nil {
		return domain.VolumeList{}, err
	}
	quota, err := s.repo.VolumeQuota(ctx)
	if err != nil {
		return domain.VolumeList{}, err
	}
	return domain.VolumeList{Volumes: volumes, Quota: quota}, nil
}

func (s *Service) ResizeVolume(ctx context.Context, name, size string) error {
	return s.repo.ResizeVolume(ctx, name, size)
}

func (s *Service) DeleteVolume(ctx context.Context, name string) error {
	return s.repo.DeleteVolume(ctx, name)
}

func (s *Service) CreateEnvironment(ctx context.Context, req domain.EnvRequest, owner, token string) (domain.Environment, error) {
	if strings.TrimSpace(req.Name) == "" {
		return domain.Environment{}, fmt.Errorf("environment name required")
	}
	profile, ok := s.registry.Get(req.Profile)
	if !ok {
		return domain.Environment{}, fmt.Errorf("unknown profile: %s", req.Profile)
	}
	return s.repo.EnsureEnvironment(ctx, domain.EnvSpec{
		ID:      req.Name,
		Profile: profile,
		Owner:   owner,
		Image:   req.Image,
		Env:     req.Env,
		Size:    req.Size,
		Token:   token,
	})
}

func (s *Service) Environments(ctx context.Context) ([]domain.Environment, error) {
	return s.repo.ListEnvironments(ctx)
}

func (s *Service) ActivateEnvironment(ctx context.Context, id string) error {
	if err := s.ensureUserEnvCapacity(ctx, id); err != nil {
		return err
	}
	return s.repo.SetEnvironmentActive(ctx, id, true)
}

func (s *Service) ensureUserEnvCapacity(ctx context.Context, id string) error {
	limit := s.limits.MaxEnvsPerUser
	if limit <= 0 {
		return nil
	}
	owner, err := s.repo.EnvironmentOwner(ctx, id)
	if err != nil {
		return err
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil
	}
	envs, err := s.repo.ListEnvironments(ctx)
	if err != nil {
		return err
	}
	active := 0
	for _, env := range envs {
		if !env.Active {
			continue
		}
		if strings.EqualFold(env.ID, id) {
			return nil
		}
		if strings.EqualFold(strings.TrimSpace(env.Owner), owner) {
			active++
		}
	}
	if active >= limit {
		return &LimitError{Message: fmt.Sprintf("you already have %d active environment(s) (limit %d); run `zeta deactivate --name <env>` first", active, limit)}
	}
	return nil
}

func (s *Service) DeactivateEnvironment(ctx context.Context, id string) error {
	return s.repo.SetEnvironmentActive(ctx, id, false)
}

func (s *Service) DeleteEnvironment(ctx context.Context, id string) error {
	return s.repo.DeleteEnvironment(ctx, id)
}

func (s *Service) EnvironmentOwner(ctx context.Context, id string) (string, error) {
	return s.repo.EnvironmentOwner(ctx, id)
}

func (s *Service) EnvironmentToken(ctx context.Context, id string) (string, error) {
	return s.repo.EnvironmentToken(ctx, id)
}

func (s *Service) ExecEnvironment(ctx context.Context, id string, opts domain.ExecOptions) error {
	return s.repo.ExecEnvironment(ctx, id, opts)
}

func (s *Service) EnsureEnvironmentIDE(ctx context.Context, id string) error {
	return s.repo.EnsureEnvironmentIDE(ctx, id)
}

func (s *Service) ForwardEnvironmentIDE(ctx context.Context, id string) (domain.PortForward, error) {
	return s.repo.ForwardEnvironmentIDE(ctx, id)
}

func (s *Service) ForwardEnvironmentPort(ctx context.Context, id string, port int) (domain.PortForward, error) {
	return s.repo.ForwardEnvironmentPort(ctx, id, port)
}

func ParseMetrics(log string) domain.CacheStats {
	stats := domain.CacheStats{Extra: map[string]string{}}
	for _, line := range strings.Split(log, "\n") {
		idx := strings.Index(line, "ZETA_METRIC ")
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len("ZETA_METRIC "):])
		parts := strings.SplitN(rest, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if value == "" {
			continue
		}
		switch key {
		case "ccache_hits":
			stats.Hits, _ = strconv.ParseInt(value, 10, 64)
		case "ccache_misses":
			stats.Misses, _ = strconv.ParseInt(value, 10, 64)
		case "cache_bytes":
			stats.SizeBytes, _ = strconv.ParseInt(value, 10, 64)
		default:
			stats.Extra[key] = value
		}
	}
	total := stats.Hits + stats.Misses
	if total > 0 {
		stats.HitRate = float64(stats.Hits) / float64(total) * 100
	}
	if len(stats.Extra) == 0 {
		stats.Extra = nil
	}
	return stats
}
