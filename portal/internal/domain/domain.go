package domain

import (
	"context"
	"io"
	"time"
)

type BuildStatus string

const (
	StatusPending   BuildStatus = "pending"
	StatusRunning   BuildStatus = "running"
	StatusSucceeded BuildStatus = "succeeded"
	StatusFailed    BuildStatus = "failed"
	StatusUnknown   BuildStatus = "unknown"
)

type Build struct {
	ID              string        `json:"id"`
	Profile         string        `json:"profile"`
	Language        string        `json:"language"`
	Repo            string        `json:"repo"`
	Commit          string        `json:"commit"`
	Owner           string        `json:"owner"`
	Status          BuildStatus   `json:"status"`
	Pod             string        `json:"pod"`
	CreatedAt       time.Time     `json:"createdAt"`
	StartedAt       *time.Time    `json:"startedAt,omitempty"`
	FinishedAt      *time.Time    `json:"finishedAt,omitempty"`
	Duration        time.Duration `json:"-"`
	DurationMs      int64         `json:"durationMs"`
	InitDurationMs  int64         `json:"initDurationMs"`
	BuildDurationMs int64         `json:"buildDurationMs"`
	Metrics         CacheStats    `json:"metrics"`
}

type CacheStats struct {
	Hits      int64             `json:"hits,omitempty"`
	Misses    int64             `json:"misses,omitempty"`
	HitRate   float64           `json:"hitRate,omitempty"`
	SizeBytes int64             `json:"sizeBytes,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

type Profile struct {
	Name            string            `json:"name" yaml:"name"`
	Description     string            `json:"description" yaml:"description"`
	Language        string            `json:"language" yaml:"language"`
	Image           string            `json:"image" yaml:"image"`
	DefaultRepo     string            `json:"defaultRepo" yaml:"defaultRepo"`
	WorkDir         string            `json:"workDir" yaml:"workDir"`
	ArtifactPath    string            `json:"artifactPath" yaml:"artifactPath"`
	CacheDir        string            `json:"cacheDir" yaml:"cacheDir"`
	CachePVC        string            `json:"cachePVC,omitempty" yaml:"cachePVC,omitempty"`
	CacheSize       string            `json:"cacheSize,omitempty" yaml:"cacheSize,omitempty"`
	ArtifactsPVC    string            `json:"artifactsPVC,omitempty" yaml:"artifactsPVC,omitempty"`
	ArtifactsSize   string            `json:"artifactsSize,omitempty" yaml:"artifactsSize,omitempty"`
	StorageClass    string            `json:"storageClass,omitempty" yaml:"storageClass,omitempty"`
	ArtifactExclude []string          `json:"artifactExclude,omitempty" yaml:"artifactExclude,omitempty"`
	MaxConcurrent   int               `json:"maxConcurrent,omitempty" yaml:"maxConcurrent,omitempty"`
	Commands        []string          `json:"commands" yaml:"commands"`
	Metrics         []Metric          `json:"metrics" yaml:"metrics"`
	Resources       Resources         `json:"resources" yaml:"resources"`
	Env             map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	EnvFromSecrets  []string          `json:"envFromSecrets,omitempty" yaml:"envFromSecrets,omitempty"`
}

type Metric struct {
	Name    string `json:"name" yaml:"name"`
	Command string `json:"command" yaml:"command"`
}

type Resources struct {
	CPURequest       string `json:"cpuRequest" yaml:"cpuRequest"`
	MemoryRequest    string `json:"memoryRequest" yaml:"memoryRequest"`
	CPULimit         string `json:"cpuLimit" yaml:"cpuLimit"`
	MemoryLimit      string `json:"memoryLimit" yaml:"memoryLimit"`
	EphemeralStorage string `json:"ephemeralStorage" yaml:"ephemeralStorage"`
}

type SubmitRequest struct {
	Profile string `json:"profile"`
	Repo    string `json:"repo"`
	Commit  string `json:"commit"`
	Sync    bool   `json:"sync,omitempty"`
}

type Volume struct {
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	CapacityBytes int64      `json:"capacityBytes"`
	StorageClass  string     `json:"storageClass"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"`
	LastBuild     string     `json:"lastBuild,omitempty"`
	LastProfile   string     `json:"lastProfile,omitempty"`
	LastOwner     string     `json:"lastOwner,omitempty"`
	Resizable     bool       `json:"resizable"`
	InUse         bool       `json:"inUse"`
}

type VolumeQuota struct {
	MaxTotalBytes  int64 `json:"maxTotalBytes"`
	UsedBytes      int64 `json:"usedBytes"`
	RemainingBytes int64 `json:"remainingBytes"`
	MaxVolumeBytes int64 `json:"maxVolumeBytes"`
}

type VolumeList struct {
	Volumes []Volume    `json:"volumes"`
	Quota   VolumeQuota `json:"quota"`
}

type BuildSpec struct {
	Profile     Profile
	Repo        string
	Commit      string
	Owner       string
	ContextPath string
	SyncToken   string
}

type Environment struct {
	ID           string    `json:"id"`
	Profile      string    `json:"profile"`
	Owner        string    `json:"owner,omitempty"`
	Active       bool      `json:"active"`
	SizeBytes    int64     `json:"sizeBytes"`
	StorageClass string    `json:"storageClass"`
	CreatedAt    time.Time `json:"createdAt"`
}

type EnvSpec struct {
	ID      string
	Profile Profile
	Owner   string
	Image   string
	Env     map[string]string
	Size    string
	Token   string
}

type EnvRequest struct {
	Name    string            `json:"name"`
	Profile string            `json:"profile"`
	Image   string            `json:"image,omitempty"`
	Size    string            `json:"size,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type TerminalSize struct {
	Width  uint16
	Height uint16
}

type TerminalSizeQueue interface {
	Next() *TerminalSize
}

type ExecOptions struct {
	Container string
	Command   []string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	TTY       bool
	SizeQueue TerminalSizeQueue
}

type PortForward struct {
	Address string
	Close   func()
}

type BuildRepository interface {
	Create(ctx context.Context, spec BuildSpec) (string, error)
	StoreContext(ctx context.Context, r io.Reader) (string, error)
	DeleteContext(ctx context.Context, path string) error
	List(ctx context.Context) ([]Build, error)
	BuildOwner(ctx context.Context, id string) (string, error)
	StreamLogs(ctx context.Context, id string, w io.Writer) error
	ReadLogs(ctx context.Context, id string, tailLines int64) (string, error)
	StreamArtifact(ctx context.Context, id string, w io.Writer) error
	ListVolumes(ctx context.Context) ([]Volume, error)
	VolumeQuota(ctx context.Context) (VolumeQuota, error)
	ResizeVolume(ctx context.Context, name, size string) error
	DeleteVolume(ctx context.Context, name string) error
	EnsureEnvironment(ctx context.Context, spec EnvSpec) (Environment, error)
	ListEnvironments(ctx context.Context) ([]Environment, error)
	SetEnvironmentActive(ctx context.Context, id string, active bool) error
	DeleteEnvironment(ctx context.Context, id string) error
	EnvironmentOwner(ctx context.Context, id string) (string, error)
	EnvironmentToken(ctx context.Context, id string) (string, error)
	ExecEnvironment(ctx context.Context, id string, opts ExecOptions) error
	EnsureEnvironmentIDE(ctx context.Context, id string) error
	ForwardEnvironmentIDE(ctx context.Context, id string) (PortForward, error)
}
