package builds

import (
	_ "embed"
	"fmt"
	"os"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"zeta/portal/internal/domain"
)

//go:embed defaults.yaml
var defaultProfiles []byte

type profileDocument struct {
	Profiles []domain.Profile `json:"profiles" yaml:"profiles"`
}

type Registry struct {
	profiles map[string]domain.Profile
}

func LoadProfiles(path string) (*Registry, error) {
	reg := &Registry{profiles: map[string]domain.Profile{}}
	if err := reg.loadBytes(defaultProfiles); err != nil {
		return nil, fmt.Errorf("default profiles: %w", err)
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("lecture profils %s: %w", path, err)
		}
		if err := reg.loadBytes(data); err != nil {
			return nil, fmt.Errorf("profils %s: %w", path, err)
		}
	}
	if len(reg.profiles) == 0 {
		return nil, fmt.Errorf("aucun profil de build")
	}
	return reg, nil
}

func (r *Registry) loadBytes(data []byte) error {
	var doc profileDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	for _, p := range doc.Profiles {
		if err := validateProfile(p); err != nil {
			return fmt.Errorf("profil %q: %w", p.Name, err)
		}
		r.profiles[p.Name] = normalizeProfile(p)
	}
	return nil
}

func validateProfile(p domain.Profile) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("name requis")
	}
	if strings.TrimSpace(p.Language) == "" {
		return fmt.Errorf("language requis")
	}
	if strings.TrimSpace(p.Image) == "" {
		return fmt.Errorf("image requise")
	}
	if len(p.Commands) == 0 {
		return fmt.Errorf("commands requis")
	}
	if p.ArtifactPath == "" {
		return fmt.Errorf("artifactPath requis")
	}
	for _, m := range p.Metrics {
		if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Command) == "" {
			return fmt.Errorf("metric invalide: name et command requis")
		}
	}
	for name := range p.Env {
		if !validEnvName(name) {
			return fmt.Errorf("variable d'environnement invalide: %q", name)
		}
	}
	for _, name := range p.EnvFromSecrets {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("envFromSecrets contient un nom vide")
		}
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func normalizeProfile(p domain.Profile) domain.Profile {
	if p.WorkDir == "" {
		p.WorkDir = "/workspace"
	}
	return p
}

func (r *Registry) Get(name string) (domain.Profile, bool) {
	p, ok := r.profiles[name]
	return p, ok
}

func (r *Registry) List() []domain.Profile {
	out := make([]domain.Profile, 0, len(r.profiles))
	for _, p := range r.profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
