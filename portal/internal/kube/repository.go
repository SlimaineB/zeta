package kube

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	appclient "k8s.io/client-go/kubernetes/typed/apps/v1"
	batchclient "k8s.io/client-go/kubernetes/typed/batch/v1"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	storagev1 "k8s.io/client-go/kubernetes/typed/storage/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/client-go/util/retry"

	"zeta/portal/internal/domain"
)

const (
	artifactsPVC          = "zeta-poc-artifacts"
	cachePVC              = "zeta-poc-cache"
	contextPVC            = "zeta-poc-contexts"
	managedBy             = "zeta.io/managed-by"
	managedValue          = "portal"
	volumeRoleLabel       = "zeta.io/volume-role"
	lastUsedAnnotation    = "zeta.io/last-used-at"
	lastBuildAnnotation   = "zeta.io/last-build"
	lastProfileAnnotation = "zeta.io/last-profile"
	lastOwnerAnnotation   = "zeta.io/last-owner"
	ownerAnnotation       = "zeta.io/owner"
	defaultArtifactsSize  = "1Gi"
	defaultCacheSize      = "2Gi"
	defaultContextSize    = "2Gi"
	defaultEnvSize        = "5Gi"
	envLabel              = "zeta.io/env"
	envProfileAnnotation  = "zeta.io/env-profile"
	envTokenKey           = "token"
	envContainerName      = "env"
	ideContainerName      = "ide"
	ideHomeVolume         = "ide-home"
	idePort               = 8080
)

type Repository struct {
	core             coreclient.CoreV1Interface
	batch            batchclient.BatchV1Interface
	storage          storagev1.StorageV1Interface
	apps             appclient.AppsV1Interface
	config           *rest.Config
	namespace        string
	contextDir       string
	storageClass     string
	volumeQuotaBytes int64
	volumeMaxBytes   int64
	imageRegistry    string
	imageTag         string
	ideImage         string
}

func NewRepository(namespace, kubeconfig, masterURL, storageClass string, volumeQuotaBytes, volumeMaxBytes int64, imageRegistry, imageTag, ideImage string) (*Repository, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if masterURL != "" {
		overrides.ClusterInfo = clientcmdapi.Cluster{Server: masterURL}
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	if storageClass == "" {
		storageClass = "local-path"
	}
	return &Repository{
		core:             clientset.CoreV1(),
		batch:            clientset.BatchV1(),
		storage:          clientset.StorageV1(),
		apps:             clientset.AppsV1(),
		config:           cfg,
		namespace:        namespace,
		contextDir:       "/contexts",
		storageClass:     storageClass,
		volumeQuotaBytes: volumeQuotaBytes,
		volumeMaxBytes:   volumeMaxBytes,
		imageRegistry:    strings.TrimSuffix(strings.TrimSpace(imageRegistry), "/"),
		imageTag:         strings.TrimSpace(imageTag),
		ideImage:         strings.TrimSpace(ideImage),
	}, nil
}

func (r *Repository) helperImage(repository, defaultTag string) string {
	tag := r.imageTag
	if tag == "" {
		tag = defaultTag
	}
	registry := strings.TrimSuffix(r.imageRegistry, "/")
	if registry == "" {
		return repository + ":" + tag
	}
	return registry + "/" + repository + ":" + tag
}

func (r *Repository) EnsurePVCs(ctx context.Context) error {
	for _, p := range []struct {
		name string
		size string
		role string
	}{
		{artifactsPVC, defaultArtifactsSize, "artifacts"},
		{cachePVC, defaultCacheSize, "cache"},
		{contextPVC, defaultContextSize, "context"},
	} {
		if err := r.ensurePVC(ctx, p.name, p.size, p.role, r.storageClass); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) ensurePVC(ctx context.Context, name, size, role, storageClass string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("nom de PVC vide")
	}
	if strings.TrimSpace(size) == "" {
		size = defaultCacheSize
	}
	if strings.TrimSpace(storageClass) == "" {
		storageClass = orDefault(r.storageClass, "local-path")
	}
	existing, err := r.core.PersistentVolumeClaims(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return r.ensureVolumeMetadata(ctx, existing, role)
	}
	if !apierrs.IsNotFound(err) {
		return err
	}
	q, err := resource.ParseQuantity(size)
	if err != nil {
		return fmt.Errorf("taille du PVC %s invalide: %w", name, err)
	}
	if r.volumeMaxBytes > 0 && q.Value() > r.volumeMaxBytes {
		return fmt.Errorf("volume %s trop grand: %s (maximum %s)", name, size, formatBytes(r.volumeMaxBytes))
	}
	if r.volumeQuotaBytes > 0 {
		used, err := r.volumeUsage(ctx)
		if err != nil {
			return err
		}
		if used+q.Value() > r.volumeQuotaBytes {
			return fmt.Errorf("volume quota exceeded: %s used, %s requested, maximum %s", formatBytes(used), formatBytes(q.Value()), formatBytes(r.volumeQuotaBytes))
		}
	}
	_, err = r.core.PersistentVolumeClaims(r.namespace).Create(ctx, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				managedBy:       managedValue,
				volumeRoleLabel: role,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: q},
			},
			StorageClassName: ptr(storageClass),
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrs.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *Repository) ensureVolumeMetadata(ctx context.Context, pvc *corev1.PersistentVolumeClaim, role string) error {
	labels := map[string]string{}
	for k, v := range pvc.Labels {
		labels[k] = v
	}
	if labels[managedBy] == managedValue && (role == "" || labels[volumeRoleLabel] == role) {
		return nil
	}
	labels[managedBy] = managedValue
	if role != "" {
		labels[volumeRoleLabel] = role
	}
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"labels": labels}})
	if err != nil {
		return err
	}
	_, err = r.core.PersistentVolumeClaims(r.namespace).Patch(ctx, pvc.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Repository) markVolumesUsed(ctx context.Context, names []string, build, profile, owner string) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, name := range names {
		patch, err := json.Marshal(map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]string{
					lastUsedAnnotation:    now,
					lastBuildAnnotation:   build,
					lastProfileAnnotation: profile,
					lastOwnerAnnotation:   owner,
				},
			},
		})
		if err != nil {
			continue
		}
		_, _ = r.core.PersistentVolumeClaims(r.namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
}

func volumeRole(name string) string {
	switch name {
	case artifactsPVC:
		return "artifacts"
	case cachePVC:
		return "cache"
	case contextPVC:
		return "context"
	default:
		return "custom"
	}
}

func (r *Repository) Create(ctx context.Context, spec domain.BuildSpec) (string, error) {
	if err := r.EnsurePVCs(ctx); err != nil {
		return "", err
	}
	profile := spec.Profile
	cacheName := orDefault(profile.CachePVC, cachePVC)
	artifactsName := orDefault(profile.ArtifactsPVC, artifactsPVC)
	if err := r.ensurePVC(ctx, cacheName, orDefault(profile.CacheSize, defaultCacheSize), "cache", profile.StorageClass); err != nil {
		return "", err
	}
	if err := r.ensurePVC(ctx, artifactsName, orDefault(profile.ArtifactsSize, defaultArtifactsSize), "artifacts", profile.StorageClass); err != nil {
		return "", err
	}
	name := jobName(profile.Name)
	checkout := ""
	if spec.Commit != "" && spec.Commit != "HEAD" {
		checkout = "git checkout --detach " + shellQuote(spec.Commit) + " && "
	}
	clone := fmt.Sprintf("git config --global --add safe.directory /workspace && %s git clone %s /workspace && cd /workspace && %sgit rev-parse HEAD", gitCredentialHelper(), shellQuote(spec.Repo), checkout)
	annotations := map[string]string{
		"zeta.io/profile":       profile.Name,
		"zeta.io/language":      profile.Language,
		"zeta.io/repo":          spec.Repo,
		"zeta.io/commit":        spec.Commit,
		ownerAnnotation:         spec.Owner,
		"zeta.io/artifact-path": profile.ArtifactPath,
		"zeta.io/cache-pvc":     cacheName,
		"zeta.io/artifacts-pvc": artifactsName,
	}
	if len(profile.ArtifactExclude) > 0 {
		if raw, err := json.Marshal(profile.ArtifactExclude); err == nil {
			annotations["zeta.io/artifact-exclude"] = string(raw)
		}
	}
	podAnnotations := map[string]string{
		"zeta.io/repo":   spec.Repo,
		"zeta.io/commit": spec.Commit,
		ownerAnnotation:  spec.Owner,
	}
	if spec.ContextPath != "" {
		annotations["zeta.io/context-path"] = spec.ContextPath
		podAnnotations["zeta.io/context-path"] = spec.ContextPath
	}
	if spec.SyncToken != "" {
		annotations["zeta.io/sync"] = "true"
		podAnnotations["zeta.io/sync"] = "true"
	}

	initContainer := corev1.Container{
		Name:            "context",
		Image:           r.helperImage("zeta/git-server", "0.1"),
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"sh", "-c"},
		SecurityContext: lockedContext(),
		Env:             profileEnv(profile),
		EnvFrom:         profileEnvFrom(profile),
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:              quantity("50m"),
				corev1.ResourceMemory:           quantity("64Mi"),
				corev1.ResourceEphemeralStorage: quantity("256Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:              quantity("1"),
				corev1.ResourceMemory:           quantity("512Mi"),
				corev1.ResourceEphemeralStorage: quantity("2Gi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}},
	}
	if spec.ContextPath != "" {
		initContainer.Args = []string{"tar -xzf " + shellQuote(spec.ContextPath) + " -C /workspace"}
		initContainer.VolumeMounts = append(initContainer.VolumeMounts, corev1.VolumeMount{Name: "context", MountPath: "/contexts", ReadOnly: true})
	} else {
		initContainer.Args = []string{clone}
	}

	buildCmd := buildScript(profile)
	if spec.SyncToken != "" {
		buildCmd = "while [ ! -f /workspace/.zeta-ready ]; do sleep 0.2; done\n" + buildCmd
	}
	containers := []corev1.Container{{
		Name:            "build",
		Image:           profile.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		WorkingDir:      profile.WorkDir,
		Command:         []string{"sh", "-c"},
		Args:            []string{buildCmd},
		SecurityContext: lockedContext(),
		Env:             profileEnv(profile),
		EnvFrom:         profileEnvFrom(profile),
		Resources:       resources(profile.Resources),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "artifacts", MountPath: "/artifacts"},
			{Name: "cache", MountPath: "/cache"},
		},
	}}
	if spec.SyncToken != "" {
		containers = append(containers, corev1.Container{
			Name:            "syncd",
			Image:           r.helperImage("zeta/portal", "0.1"),
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/syncd"},
			SecurityContext: lockedContext(),
			Env: []corev1.EnvVar{
				{Name: "ZETA_SYNC_TOKEN", Value: spec.SyncToken},
				{Name: "ZETA_SYNC_URL", Value: fmt.Sprintf("ws://zeta-portal/api/sync/%s/agent", name)},
				{Name: "ZETA_WORKSPACE", Value: "/workspace"},
				{Name: "ZETA_ARTIFACTS", Value: profile.ArtifactPath},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    quantity("10m"),
					corev1.ResourceMemory: quantity("32Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    quantity("100m"),
					corev1.ResourceMemory: quantity("128Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "workspace", MountPath: "/workspace"},
				{Name: "artifacts", MountPath: "/artifacts"},
			},
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.namespace,
			Labels: map[string]string{
				managedBy:          managedValue,
				"zeta.io/profile":  profile.Name,
				"zeta.io/language": profile.Language,
			},
			Annotations: annotations,
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: ptrI32(3600),
			BackoffLimit:            ptrI32(0),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						managedBy:          managedValue,
						"zeta.io/profile":  profile.Name,
						"zeta.io/language": profile.Language,
					},
					Annotations: podAnnotations,
				},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: ptrB(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptrB(true),
						RunAsUser:      ptrI64(1000),
						RunAsGroup:     ptrI64(1000),
						FSGroup:        ptrI64(1000),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					InitContainers: []corev1.Container{initContainer},
					Containers:     containers,
					Volumes: []corev1.Volume{
						{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: quantityPtr("2Gi")}}},
						{Name: "artifacts", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: artifactsName}}},
						{Name: "cache", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: cacheName}}},
						{Name: "context", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: contextPVC}}},
					},
				},
			},
		},
	}
	deadline := int64(3600)
	if spec.SyncToken != "" {
		deadline = 1800
	}
	job.Spec.ActiveDeadlineSeconds = &deadline
	created, err := r.batch.Jobs(r.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	used := []string{artifactsName, cacheName}
	if spec.ContextPath != "" {
		used = append(used, contextPVC)
	}
	r.markVolumesUsed(ctx, used, created.Name, profile.Name, spec.Owner)
	return created.Name, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func gitCredentialHelper() string {
	return `if [ -n "${GIT_TOKEN:-}" ]; then git config --global credential.helper '!f() { echo username="${GIT_USERNAME:-x-access-token}"; echo password="${GIT_TOKEN}"; }; f'; fi`
}

func profileEnv(profile domain.Profile) []corev1.EnvVar {
	if len(profile.Env) == 0 {
		return nil
	}
	names := make([]string, 0, len(profile.Env))
	for name := range profile.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	env := make([]corev1.EnvVar, 0, len(names))
	for _, name := range names {
		env = append(env, corev1.EnvVar{Name: name, Value: profile.Env[name]})
	}
	return env
}

func mergeEnv(base []corev1.EnvVar, extra map[string]string) []corev1.EnvVar {
	if len(extra) == 0 {
		return base
	}
	merged := make([]corev1.EnvVar, 0, len(base)+len(extra))
	seen := make(map[string]bool, len(extra))
	for _, env := range base {
		if value, ok := extra[env.Name]; ok {
			merged = append(merged, corev1.EnvVar{Name: env.Name, Value: value})
			seen[env.Name] = true
			continue
		}
		merged = append(merged, env)
	}
	names := make([]string, 0, len(extra))
	for name := range extra {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		merged = append(merged, corev1.EnvVar{Name: name, Value: extra[name]})
	}
	return merged
}

func profileEnvFrom(profile domain.Profile) []corev1.EnvFromSource {
	if len(profile.EnvFromSecrets) == 0 {
		return nil
	}
	optional := true
	out := make([]corev1.EnvFromSource, 0, len(profile.EnvFromSecrets))
	for _, name := range profile.EnvFromSecrets {
		out = append(out, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: name},
				Optional:             &optional,
			},
		})
	}
	return out
}

func buildScript(p domain.Profile) string {
	var b strings.Builder
	b.WriteString("set -eux\n")
	b.WriteString("if command -v git >/dev/null 2>&1; then " + gitCredentialHelper() + "; fi\n")
	b.WriteString("cd " + p.WorkDir + "\n")
	for _, cmd := range p.Commands {
		b.WriteString(cmd)
		if !strings.HasSuffix(cmd, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("set +e\n")
	for _, m := range p.Metrics {
		fmt.Fprintf(&b, "echo \"ZETA_METRIC %s=$(%s)\"\n", m.Name, m.Command)
	}
	b.WriteString("set -e\n")
	return b.String()
}

func (r *Repository) BuildOwner(ctx context.Context, id string) (string, error) {
	job, err := r.batch.Jobs(r.namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return job.Annotations[ownerAnnotation], nil
}

func (r *Repository) List(ctx context.Context) ([]domain.Build, error) {
	list, err := r.batch.Jobs(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedBy + "=" + managedValue,
	})
	if err != nil {
		return nil, err
	}
	builds := make([]domain.Build, 0, len(list.Items))
	for _, job := range list.Items {
		build := domain.Build{
			ID:        job.Name,
			Profile:   job.Annotations["zeta.io/profile"],
			Language:  job.Annotations["zeta.io/language"],
			Repo:      job.Annotations["zeta.io/repo"],
			Commit:    job.Annotations["zeta.io/commit"],
			Owner:     job.Annotations[ownerAnnotation],
			CreatedAt: job.CreationTimestamp.Time,
			Status:    statusOf(&job),
		}
		if build.Profile == "" {
			build.Profile = job.Labels["zeta.io/profile"]
		}
		if build.Language == "" {
			build.Language = job.Labels["zeta.io/language"]
		}
		if job.Status.StartTime != nil {
			build.StartedAt = &job.Status.StartTime.Time
		}
		if job.Status.CompletionTime != nil {
			build.FinishedAt = &job.Status.CompletionTime.Time
		}
		if build.StartedAt != nil {
			end := time.Now()
			if build.FinishedAt != nil {
				end = *build.FinishedAt
			}
			build.Duration = end.Sub(*build.StartedAt)
			build.DurationMs = build.Duration.Milliseconds()
		}
		if pod := r.podForJob(ctx, job.Name); pod != nil {
			build.Pod = pod.Name
			build.InitDurationMs, build.BuildDurationMs = podDurations(pod)
			if job.Annotations["zeta.io/sync"] == "true" {
				if term := buildContainerTerminated(pod); term != nil {
					if term.ExitCode == 0 {
						build.Status = domain.StatusSucceeded
					} else {
						build.Status = domain.StatusFailed
					}
					build.FinishedAt = &term.FinishedAt.Time
					if build.StartedAt != nil {
						end := term.FinishedAt.Time
						build.Duration = end.Sub(*build.StartedAt)
						build.DurationMs = build.Duration.Milliseconds()
					}
				}
			}
		}
		if (job.Status.Succeeded > 0 || job.Status.Failed > 0) && job.Annotations["zeta.io/context-path"] != "" {
			_ = r.DeleteContext(ctx, job.Annotations["zeta.io/context-path"])
		}
		builds = append(builds, build)
	}
	r.cleanupContexts(contextCleanupAge)
	return builds, nil
}

func statusOf(job *batchv1.Job) domain.BuildStatus {
	switch {
	case job.Status.Succeeded > 0:
		return domain.StatusSucceeded
	case job.Status.Failed > 0:
		return domain.StatusFailed
	case job.Status.Active > 0:
		return domain.StatusRunning
	default:
		return domain.StatusPending
	}
}

func (r *Repository) podForJob(ctx context.Context, job string) *corev1.Pod {
	list, err := r.core.Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + job})
	if err != nil || len(list.Items) == 0 {
		return nil
	}
	return &list.Items[0]
}

func buildContainerTerminated(pod *corev1.Pod) *corev1.ContainerStateTerminated {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == "build" && cs.State.Terminated != nil {
			return cs.State.Terminated
		}
	}
	return nil
}

func podDurations(pod *corev1.Pod) (initMs, buildMs int64) {
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.Name == "context" && cs.State.Terminated != nil {
			initMs = cs.State.Terminated.FinishedAt.Sub(cs.State.Terminated.StartedAt.Time).Milliseconds()
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != "build" {
			continue
		}
		if cs.State.Terminated != nil {
			buildMs = cs.State.Terminated.FinishedAt.Sub(cs.State.Terminated.StartedAt.Time).Milliseconds()
		} else if cs.State.Running != nil {
			buildMs = time.Since(cs.State.Running.StartedAt.Time).Milliseconds()
		}
	}
	return initMs, buildMs
}

func (r *Repository) StreamLogs(ctx context.Context, id string, w io.Writer) error {
	var pod *corev1.Pod
	container := "build"
	err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 60*time.Second, true, func(ctx context.Context) (bool, error) {
		p := r.podForJob(ctx, id)
		if p == nil {
			return false, nil
		}
		pod = p
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Name == "build" && (cs.State.Running != nil || cs.State.Terminated != nil) {
				return true, nil
			}
		}
		if p.Status.Phase == corev1.PodFailed {
			container = "context"
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("logs indisponibles pour %s: %w", id, err)
	}
	if pod == nil {
		return fmt.Errorf("aucun pod pour le job %s", id)
	}
	req := r.core.Pods(r.namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: container, Follow: true})
	stream, err := req.Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	_, err = io.Copy(w, stream)
	return err
}

func (r *Repository) ReadLogs(ctx context.Context, id string, tailLines int64) (string, error) {
	pod := r.podForJob(ctx, id)
	if pod == nil {
		return "", fmt.Errorf("aucun pod pour le job %s", id)
	}
	req := r.core.Pods(r.namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "build", TailLines: &tailLines})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, 4<<20))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (r *Repository) VolumeQuota(ctx context.Context) (domain.VolumeQuota, error) {
	used, err := r.volumeUsage(ctx)
	if err != nil {
		return domain.VolumeQuota{}, err
	}
	quota := domain.VolumeQuota{
		MaxTotalBytes:  r.volumeQuotaBytes,
		UsedBytes:      used,
		MaxVolumeBytes: r.volumeMaxBytes,
	}
	if r.volumeQuotaBytes > 0 {
		quota.RemainingBytes = r.volumeQuotaBytes - used
		if quota.RemainingBytes < 0 {
			quota.RemainingBytes = 0
		}
	}
	return quota, nil
}

func (r *Repository) volumeUsage(ctx context.Context) (int64, error) {
	volumes, err := r.ListVolumes(ctx)
	if err != nil {
		return 0, err
	}
	var used int64
	for _, v := range volumes {
		used += v.CapacityBytes
	}
	return used, nil
}

func (r *Repository) inUseVolumes(ctx context.Context) map[string]bool {
	inUse := map[string]bool{}
	if r.batch == nil {
		return inUse
	}
	jobs, err := r.batch.Jobs(r.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return inUse
	}
	for _, job := range jobs.Items {
		if job.Status.CompletionTime != nil || job.Status.Succeeded > 0 || job.Status.Failed > 0 {
			continue
		}
		if name := job.Annotations["zeta.io/cache-pvc"]; name != "" {
			inUse[name] = true
		}
		if name := job.Annotations["zeta.io/artifacts-pvc"]; name != "" {
			inUse[name] = true
		}
		if job.Annotations["zeta.io/context-path"] != "" {
			inUse[contextPVC] = true
		}
	}
	return inUse
}

func (r *Repository) expandableStorageClasses(ctx context.Context) map[string]bool {
	expandable := map[string]bool{}
	if r.storage == nil {
		return expandable
	}
	list, err := r.storage.StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return expandable
	}
	for _, sc := range list.Items {
		expandable[sc.Name] = sc.AllowVolumeExpansion != nil && *sc.AllowVolumeExpansion
	}
	return expandable
}

func (r *Repository) managedVolume(ctx context.Context, name string) (*corev1.PersistentVolumeClaim, error) {
	pvc, err := r.core.PersistentVolumeClaims(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrs.IsNotFound(err) {
			return nil, fmt.Errorf("volume %s introuvable", name)
		}
		return nil, err
	}
	if pvc.Labels[managedBy] != managedValue && !isDefaultVolume(name) {
		return nil, fmt.Errorf("volume %s not managed by Zeta", name)
	}
	return pvc, nil
}

func isDefaultVolume(name string) bool {
	return name == artifactsPVC || name == cachePVC || name == contextPVC
}

func (r *Repository) ResizeVolume(ctx context.Context, name, size string) error {
	pvc, err := r.managedVolume(ctx, name)
	if err != nil {
		return err
	}
	if r.inUseVolumes(ctx)[name] {
		return fmt.Errorf("volume %s is in use by a running build", name)
	}
	current := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	q, err := resource.ParseQuantity(size)
	if err != nil {
		return fmt.Errorf("invalid size: %s", size)
	}
	if q.Cmp(current) <= 0 {
		return fmt.Errorf("new size must be greater than %s", current.String())
	}
	if r.volumeMaxBytes > 0 && q.Value() > r.volumeMaxBytes {
		return fmt.Errorf("volume too large: maximum %s", formatBytes(r.volumeMaxBytes))
	}
	if r.volumeQuotaBytes > 0 {
		used, err := r.volumeUsage(ctx)
		if err != nil {
			return err
		}
		delta := q.Value() - current.Value()
		if used+delta > r.volumeQuotaBytes {
			return fmt.Errorf("volume quota exceeded: %s available", formatBytes(r.volumeQuotaBytes-used))
		}
	}
	storageClass := derefString(pvc.Spec.StorageClassName)
	if storageClass == "" {
		storageClass = r.storageClass
	}
	if r.storage == nil {
		return fmt.Errorf("client Kubernetes storage indisponible")
	}
	sc, err := r.storage.StorageClasses().Get(ctx, storageClass, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("StorageClass %s introuvable: %w", storageClass, err)
	}
	if sc.AllowVolumeExpansion == nil || !*sc.AllowVolumeExpansion {
		return fmt.Errorf("StorageClass %s ne permet pas l'agrandissement (allowVolumeExpansion)", storageClass)
	}
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"resources": map[string]any{
				"requests": map[string]string{"storage": q.String()},
			},
		},
	})
	if err != nil {
		return err
	}
	_, err = r.core.PersistentVolumeClaims(r.namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Repository) DeleteVolume(ctx context.Context, name string) error {
	if _, err := r.managedVolume(ctx, name); err != nil {
		return err
	}
	if r.inUseVolumes(ctx)[name] {
		return fmt.Errorf("volume %s is in use by a running build", name)
	}
	return r.core.PersistentVolumeClaims(r.namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func envPVCName(id string) string    { return "zeta-env-" + id }
func envSecretName(id string) string { return "zeta-env-" + id + "-sync" }
func envDeployName(id string) string { return "zeta-env-" + id }

func newSyncToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generating sync token: %v", err))
	}
	return fmt.Sprintf("%x", b)
}

func (r *Repository) EnsureEnvironment(ctx context.Context, spec domain.EnvSpec) (domain.Environment, error) {
	if strings.TrimSpace(spec.ID) == "" {
		return domain.Environment{}, fmt.Errorf("environment name required")
	}
	id := slug(spec.ID)
	profile := spec.Profile
	owner := strings.TrimSpace(spec.Owner)
	size := orDefault(spec.Size, defaultEnvSize)
	storageClass := orDefault(r.storageClass, "local-path")
	if err := r.ensurePVC(ctx, envPVCName(id), size, "env", storageClass); err != nil {
		return domain.Environment{}, err
	}
	if err := r.tagEnvVolume(ctx, id); err != nil {
		return domain.Environment{}, err
	}
	cacheName := orDefault(profile.CachePVC, cachePVC)
	if err := r.ensurePVC(ctx, cacheName, orDefault(profile.CacheSize, defaultCacheSize), "cache", storageClass); err != nil {
		return domain.Environment{}, err
	}
	if _, err := r.ensureEnvToken(ctx, id, spec.Token); err != nil {
		return domain.Environment{}, err
	}
	if err := r.ensureEnvDeployment(ctx, id, profile, owner, cacheName, spec.Image, spec.Env); err != nil {
		return domain.Environment{}, err
	}
	return r.environment(ctx, id)
}

func (r *Repository) tagEnvVolume(ctx context.Context, id string) error {
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{
				managedBy:       managedValue,
				volumeRoleLabel: "env",
				envLabel:        id,
			},
		},
	})
	if err != nil {
		return err
	}
	_, err = r.core.PersistentVolumeClaims(r.namespace).Patch(ctx, envPVCName(id), types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Repository) ensureEnvToken(ctx context.Context, id, candidate string) (string, error) {
	name := envSecretName(id)
	secret, err := r.core.Secrets(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if token := string(secret.Data[envTokenKey]); token != "" {
			return token, nil
		}
	}
	if err != nil && !apierrs.IsNotFound(err) {
		return "", err
	}
	token := candidate
	if strings.TrimSpace(token) == "" {
		token = newSyncToken()
	}
	_, err = r.core.Secrets(r.namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.namespace,
			Labels:    map[string]string{managedBy: managedValue, envLabel: id},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{envTokenKey: []byte(token)},
	}, metav1.CreateOptions{})
	if err != nil {
		if apierrs.IsAlreadyExists(err) {
			existing, getErr := r.core.Secrets(r.namespace).Get(ctx, name, metav1.GetOptions{})
			if getErr != nil {
				return "", getErr
			}
			return string(existing.Data[envTokenKey]), nil
		}
		return "", err
	}
	return token, nil
}

func (r *Repository) ensureEnvDeployment(ctx context.Context, id string, profile domain.Profile, owner, cacheName, image string, extraEnv map[string]string) error {
	name := envDeployName(id)
	labels := map[string]string{
		managedBy:         managedValue,
		envLabel:          id,
		"zeta.io/profile": profile.Name,
	}
	selector := map[string]string{managedBy: managedValue, envLabel: id}
	envContainer := corev1.Container{
		Name:            envContainerName,
		Image:           orDefault(image, profile.Image),
		ImagePullPolicy: corev1.PullIfNotPresent,
		WorkingDir:      orDefault(profile.WorkDir, "/workspace"),
		Command:         []string{"sleep", "infinity"},
		SecurityContext: lockedContext(),
		Env:             mergeEnv(profileEnv(profile), extraEnv),
		EnvFrom:         profileEnvFrom(profile),
		Resources:       resources(profile.Resources),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "cache", MountPath: "/cache"},
		},
	}
	syncdContainer := corev1.Container{
		Name:            "syncd",
		Image:           r.helperImage("zeta/portal", "0.1"),
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/syncd"},
		SecurityContext: lockedContext(),
		Env: []corev1.EnvVar{
			{
				Name: "ZETA_SYNC_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: envSecretName(id)},
						Key:                  envTokenKey,
					},
				},
			},
			{Name: "ZETA_SYNC_URL", Value: fmt.Sprintf("ws://zeta-portal/api/sync/%s/agent", id)},
			{Name: "ZETA_WORKSPACE", Value: "/workspace"},
			{Name: "ZETA_SYNC_ENV", Value: "1"},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    quantity("10m"),
				corev1.ResourceMemory: quantity("32Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    quantity("100m"),
				corev1.ResourceMemory: quantity("128Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}},
	}
	containers := []corev1.Container{envContainer, syncdContainer}
	volumes := []corev1.Volume{
		{Name: "workspace", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: envPVCName(id)}}},
		{Name: "cache", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: cacheName}}},
	}
	if r.ideEnabled() {
		containers = append(containers, r.ideContainer())
		volumes = append(volumes, corev1.Volume{Name: ideHomeVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   r.namespace,
			Labels:      labels,
			Annotations: map[string]string{envProfileAnnotation: profile.Name, ownerAnnotation: owner},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptrI32(0),
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptrB(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptrB(true),
						RunAsUser:      ptrI64(1000),
						RunAsGroup:     ptrI64(1000),
						FSGroup:        ptrI64(1000),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: containers,
					Volumes:    volumes,
				},
			},
		},
	}
	existing, err := r.apps.Deployments(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrs.IsNotFound(err) {
		_, err = r.apps.Deployments(r.namespace).Create(ctx, deployment, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Labels = deployment.Labels
	existing.Annotations = deployment.Annotations
	existing.Spec.Template = deployment.Spec.Template
	_, err = r.apps.Deployments(r.namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (r *Repository) environment(ctx context.Context, id string) (domain.Environment, error) {
	dep, err := r.apps.Deployments(r.namespace).Get(ctx, envDeployName(id), metav1.GetOptions{})
	if err != nil {
		return domain.Environment{}, err
	}
	env := domain.Environment{
		ID:        id,
		Profile:   dep.Labels["zeta.io/profile"],
		Owner:     dep.Annotations[ownerAnnotation],
		Active:    dep.Spec.Replicas != nil && *dep.Spec.Replicas > 0,
		CreatedAt: dep.CreationTimestamp.Time,
	}
	if pvc, err := r.core.PersistentVolumeClaims(r.namespace).Get(ctx, envPVCName(id), metav1.GetOptions{}); err == nil {
		env.StorageClass = derefString(pvc.Spec.StorageClassName)
		if q, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			env.SizeBytes = q.Value()
		}
	}
	return env, nil
}

func (r *Repository) ListEnvironments(ctx context.Context) ([]domain.Environment, error) {
	list, err := r.apps.Deployments(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedBy + "=" + managedValue + "," + envLabel,
	})
	if err != nil {
		return nil, err
	}
	envs := make([]domain.Environment, 0, len(list.Items))
	for i := range list.Items {
		dep := &list.Items[i]
		id := dep.Labels[envLabel]
		if id == "" {
			continue
		}
		env := domain.Environment{
			ID:        id,
			Profile:   dep.Labels["zeta.io/profile"],
			Owner:     dep.Annotations[ownerAnnotation],
			Active:    dep.Spec.Replicas != nil && *dep.Spec.Replicas > 0,
			CreatedAt: dep.CreationTimestamp.Time,
		}
		if pvc, err := r.core.PersistentVolumeClaims(r.namespace).Get(ctx, envPVCName(id), metav1.GetOptions{}); err == nil {
			env.StorageClass = derefString(pvc.Spec.StorageClassName)
			if q, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
				env.SizeBytes = q.Value()
			}
		}
		envs = append(envs, env)
	}
	sort.Slice(envs, func(i, j int) bool { return envs[i].ID < envs[j].ID })
	return envs, nil
}

func (r *Repository) SetEnvironmentActive(ctx context.Context, id string, active bool) error {
	replicas := int32(0)
	if active {
		replicas = 1
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"replicas": replicas}})
	if err != nil {
		return err
	}
	_, err = r.apps.Deployments(r.namespace).Patch(ctx, envDeployName(id), types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (r *Repository) DeleteEnvironment(ctx context.Context, id string) error {
	_ = r.apps.Deployments(r.namespace).Delete(ctx, envDeployName(id), metav1.DeleteOptions{})
	if pods, err := r.core.Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: envLabel + "=" + id}); err == nil {
		for _, pod := range pods.Items {
			_ = r.core.Pods(r.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		}
	}
	_ = wait.PollUntilContextTimeout(ctx, time.Second, 45*time.Second, true, func(ctx context.Context) (bool, error) {
		pods, err := r.core.Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: envLabel + "=" + id})
		if err != nil {
			return false, nil
		}
		return len(pods.Items) == 0, nil
	})
	_ = r.core.Secrets(r.namespace).Delete(ctx, envSecretName(id), metav1.DeleteOptions{})
	if err := r.core.PersistentVolumeClaims(r.namespace).Delete(ctx, envPVCName(id), metav1.DeleteOptions{}); err != nil && !apierrs.IsNotFound(err) {
		return err
	}
	err := wait.PollUntilContextTimeout(ctx, time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := r.core.PersistentVolumeClaims(r.namespace).Get(ctx, envPVCName(id), metav1.GetOptions{})
		if apierrs.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("environment %s was removed but its volume is still terminating", id)
	}
	return nil
}

func (r *Repository) EnvironmentOwner(ctx context.Context, id string) (string, error) {
	dep, err := r.apps.Deployments(r.namespace).Get(ctx, envDeployName(id), metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return dep.Annotations[ownerAnnotation], nil
}

func (r *Repository) EnvironmentToken(ctx context.Context, id string) (string, error) {
	secret, err := r.core.Secrets(r.namespace).Get(ctx, envSecretName(id), metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	token := string(secret.Data[envTokenKey])
	if token == "" {
		return "", fmt.Errorf("environment %s has no sync token", id)
	}
	return token, nil
}

func (r *Repository) ideEnabled() bool {
	return strings.TrimSpace(r.ideImage) != ""
}

func (r *Repository) ideContainer() corev1.Container {
	return corev1.Container{
		Name:            ideContainerName,
		Image:           r.ideImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"code-server"},
		Args: []string{
			"--bind-addr", fmt.Sprintf("127.0.0.1:%d", idePort),
			"--auth", "none",
			"--disable-telemetry",
			"--user-data-dir", "/home/coder/.local/share/code-server",
			"--extensions-dir", "/home/coder/.local/share/code-server/extensions",
			"/workspace",
		},
		WorkingDir:      "/workspace",
		SecurityContext: lockedContext(),
		Env: []corev1.EnvVar{
			{Name: "HOME", Value: "/home/coder"},
			{Name: "XDG_CONFIG_HOME", Value: "/home/coder/.config"},
			{Name: "XDG_DATA_HOME", Value: "/home/coder/.local/share"},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    quantity("50m"),
				corev1.ResourceMemory: quantity("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    quantity("500m"),
				corev1.ResourceMemory: quantity("512Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: ideHomeVolume, MountPath: "/home/coder"},
		},
	}
}

func (r *Repository) EnsureEnvironmentIDE(ctx context.Context, id string) error {
	if !r.ideEnabled() {
		return fmt.Errorf("IDE web désactivé sur ce portail")
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		dep, err := r.apps.Deployments(r.namespace).Get(ctx, envDeployName(id), metav1.GetOptions{})
		if err != nil {
			return err
		}
		desired := r.ideContainer()
		changed := false
		found := false
		for i := range dep.Spec.Template.Spec.Containers {
			if dep.Spec.Template.Spec.Containers[i].Name == ideContainerName {
				if ideContainerChanged(dep.Spec.Template.Spec.Containers[i], desired) {
					dep.Spec.Template.Spec.Containers[i] = desired
					changed = true
				}
				found = true
				break
			}
		}
		if !found {
			dep.Spec.Template.Spec.Containers = append(dep.Spec.Template.Spec.Containers, desired)
			changed = true
		}
		hasHome := false
		for _, volume := range dep.Spec.Template.Spec.Volumes {
			if volume.Name == ideHomeVolume {
				hasHome = true
				break
			}
		}
		if !hasHome {
			dep.Spec.Template.Spec.Volumes = append(dep.Spec.Template.Spec.Volumes, corev1.Volume{
				Name:         ideHomeVolume,
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			})
			changed = true
		}
		if !changed {
			return nil
		}
		_, err = r.apps.Deployments(r.namespace).Update(ctx, dep, metav1.UpdateOptions{})
		return err
	})
}

func ideContainerChanged(existing, desired corev1.Container) bool {
	if existing.ReadinessProbe != nil || existing.LivenessProbe != nil || existing.StartupProbe != nil {
		return true
	}
	return existing.Image != desired.Image ||
		!reflect.DeepEqual(existing.Command, desired.Command) ||
		!reflect.DeepEqual(existing.Args, desired.Args) ||
		existing.WorkingDir != desired.WorkingDir ||
		!reflect.DeepEqual(existing.Env, desired.Env) ||
		!reflect.DeepEqual(existing.Resources, desired.Resources) ||
		!reflect.DeepEqual(existing.VolumeMounts, desired.VolumeMounts) ||
		!reflect.DeepEqual(existing.SecurityContext, desired.SecurityContext)
}

func (r *Repository) ForwardEnvironmentIDE(ctx context.Context, id string) (domain.PortForward, error) {
	pod, err := r.waitIDEPod(ctx, id)
	if err != nil {
		return domain.PortForward{}, err
	}
	return r.forwardPodPort(ctx, pod, idePort)
}

func (r *Repository) waitIDEPod(ctx context.Context, id string) (string, error) {
	deadline := time.Now().Add(90 * time.Second)
	for {
		pod, err := r.envIDEPod(ctx, id)
		if err == nil {
			return pod, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if time.Now().After(deadline) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (r *Repository) envIDEPod(ctx context.Context, id string) (string, error) {
	pods, err := r.core.Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: envLabel + "=" + id})
	if err != nil {
		return "", err
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		ready := map[string]bool{}
		for _, status := range pod.Status.ContainerStatuses {
			ready[status.Name] = status.Ready && status.State.Running != nil
		}
		if ready[envContainerName] && ready[ideContainerName] {
			return pod.Name, nil
		}
	}
	return "", fmt.Errorf("IDE de l'environnement %s pas encore prêt", id)
}

func (r *Repository) forwardPodPort(ctx context.Context, pod string, port int) (domain.PortForward, error) {
	if r.config == nil {
		return domain.PortForward{}, fmt.Errorf("configuration Kubernetes indisponible")
	}
	transport, upgrader, err := spdy.RoundTripperFor(r.config)
	if err != nil {
		return domain.PortForward{}, err
	}
	target, err := url.Parse(r.config.Host)
	if err != nil {
		return domain.PortForward{}, err
	}
	if target.Scheme == "" {
		target.Scheme = "https"
	}
	target.Path = fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", r.namespace, pod)
	target.RawQuery = ""
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, target)
	stop := make(chan struct{})
	ready := make(chan struct{}, 1)
	forwarder, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("0:%d", port)}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return domain.PortForward{}, err
	}
	go func() { _ = forwarder.ForwardPorts() }()
	select {
	case <-ctx.Done():
		close(stop)
		return domain.PortForward{}, ctx.Err()
	case <-ready:
	}
	ports, err := forwarder.GetPorts()
	if err != nil {
		close(stop)
		return domain.PortForward{}, err
	}
	if len(ports) == 0 {
		close(stop)
		return domain.PortForward{}, fmt.Errorf("port-forward IDE sans port local")
	}
	var once sync.Once
	return domain.PortForward{
		Address: fmt.Sprintf("127.0.0.1:%d", ports[0].Local),
		Close:   func() { once.Do(func() { close(stop) }) },
	}, nil
}

func (r *Repository) ExecEnvironment(ctx context.Context, id string, opts domain.ExecOptions) error {
	pod, err := r.envPod(ctx, id)
	if err != nil {
		return err
	}
	container := orDefault(opts.Container, envContainerName)
	req := r.core.RESTClient().Post().
		Resource("pods").
		Namespace(r.namespace).
		Name(pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   opts.Command,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil,
			TTY:       opts.TTY,
		}, scheme.ParameterCodec)
	executor, err := remotecommand.NewWebSocketExecutor(r.config, "GET", req.URL().String())
	if err != nil {
		return err
	}
	streamOpts := remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    opts.TTY,
	}
	if opts.SizeQueue != nil {
		streamOpts.TerminalSizeQueue = terminalSizeAdapter{queue: opts.SizeQueue}
	}
	return executor.StreamWithContext(ctx, streamOpts)
}

func (r *Repository) envPod(ctx context.Context, id string) (string, error) {
	pods, err := r.core.Pods(r.namespace).List(ctx, metav1.ListOptions{LabelSelector: envLabel + "=" + id})
	if err != nil {
		return "", err
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == envContainerName && status.State.Running != nil {
				return pod.Name, nil
			}
		}
	}
	return "", fmt.Errorf("environment %s is not running", id)
}

type terminalSizeAdapter struct {
	queue domain.TerminalSizeQueue
}

func (a terminalSizeAdapter) Next() *remotecommand.TerminalSize {
	size := a.queue.Next()
	if size == nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: size.Width, Height: size.Height}
}

func formatBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := float64(n)
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	if value >= 10 {
		return fmt.Sprintf("%.0f %s", value, units[i])
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}

func (r *Repository) ListVolumes(ctx context.Context) ([]domain.Volume, error) {
	list, err := r.core.PersistentVolumeClaims(r.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	volumes := make([]domain.Volume, 0, len(list.Items))
	inUse := r.inUseVolumes(ctx)
	expandable := r.expandableStorageClasses(ctx)
	for _, pvc := range list.Items {
		role := pvc.Labels[volumeRoleLabel]
		managed := pvc.Labels[managedBy] == managedValue
		if role == "" {
			switch pvc.Name {
			case artifactsPVC:
				role = "artifacts"
			case cachePVC:
				role = "cache"
			case contextPVC:
				role = "context"
			}
		}
		if role == "" && !managed {
			continue
		}
		if role == "" {
			role = "custom"
		}
		capacity := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if q, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
			capacity = q
		}
		storageClass := derefString(pvc.Spec.StorageClassName)
		volume := domain.Volume{
			Name:          pvc.Name,
			Role:          role,
			CapacityBytes: capacity.Value(),
			StorageClass:  storageClass,
			CreatedAt:     pvc.CreationTimestamp.Time,
			LastBuild:     pvc.Annotations[lastBuildAnnotation],
			LastProfile:   pvc.Annotations[lastProfileAnnotation],
			LastOwner:     pvc.Annotations[lastOwnerAnnotation],
			Resizable:     expandable[storageClass],
			InUse:         inUse[pvc.Name],
		}
		if raw := pvc.Annotations[lastUsedAnnotation]; raw != "" {
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				volume.LastUsedAt = &t
			}
		}
		volumes = append(volumes, volume)
	}
	sort.Slice(volumes, func(i, j int) bool {
		if volumes[i].Role == volumes[j].Role {
			return volumes[i].Name < volumes[j].Name
		}
		return volumes[i].Role < volumes[j].Role
	})
	return volumes, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *Repository) StreamArtifact(ctx context.Context, id string, w io.Writer) error {
	job, err := r.batch.Jobs(r.namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if job.Status.Succeeded == 0 {
		return fmt.Errorf("build %s did not succeed", id)
	}
	artifactPath := job.Annotations["zeta.io/artifact-path"]
	if artifactPath == "" {
		artifactPath = "/artifacts/" + job.Annotations["zeta.io/language"]
	}
	artifactsClaim := job.Annotations["zeta.io/artifacts-pvc"]
	if artifactsClaim == "" {
		artifactsClaim = artifactsPVC
	}
	var excludes []string
	if raw := job.Annotations["zeta.io/artifact-exclude"]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &excludes)
	}
	name := artifactPodName(id)
	_ = r.core.Pods(r.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: r.namespace, Labels: map[string]string{
			managedBy:       managedValue,
			"zeta.io/build": id,
		}},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: ptrB(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptrB(true),
				RunAsUser:      ptrI64(1000),
				RunAsGroup:     ptrI64(1000),
				FSGroup:        ptrI64(1000),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:            "artifact",
				Image:           "busybox:1.36",
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"sh", "-c"},
				Args:            []string{artifactTarCommand(artifactPath, excludes)},
				SecurityContext: lockedContext(),
				VolumeMounts:    []corev1.VolumeMount{{Name: "artifacts", MountPath: "/artifacts"}},
			}},
			Volumes: []corev1.Volume{{
				Name:         "artifacts",
				VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: artifactsClaim}},
			}},
		},
	}
	if _, err := r.core.Pods(r.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return err
	}
	defer r.core.Pods(r.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 60*time.Second, true, func(ctx context.Context) (bool, error) {
		p, err := r.core.Pods(r.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed, nil
	}); err != nil {
		return err
	}
	p, err := r.core.Pods(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if p.Status.Phase == corev1.PodFailed {
		return fmt.Errorf("artifact retrieval failed: %s", p.Status.Message)
	}
	stream, err := r.core.Pods(r.namespace).GetLogs(name, &corev1.PodLogOptions{Container: "artifact"}).Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	gz, err := gzip.NewReader(base64.NewDecoder(base64.StdEncoding, stream))
	if err != nil {
		return fmt.Errorf("decoding artifacts: %w", err)
	}
	defer gz.Close()
	_, err = io.Copy(w, gz)
	return err
}

func artifactTarCommand(artifactPath string, excludes []string) string {
	cmd := "tar -C " + shellQuote(artifactPath)
	for _, pattern := range excludes {
		if strings.TrimSpace(pattern) == "" {
			continue
		}
		cmd += " --exclude=" + shellQuote(pattern)
	}
	return cmd + " -czf - . | base64"
}

func resources(r domain.Resources) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    quantity(orDefault(r.CPURequest, "200m")),
			corev1.ResourceMemory: quantity(orDefault(r.MemoryRequest, "256Mi")),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:              quantity(orDefault(r.CPULimit, "2")),
			corev1.ResourceMemory:           quantity(orDefault(r.MemoryLimit, "2Gi")),
			corev1.ResourceEphemeralStorage: quantity(orDefault(r.EphemeralStorage, "2Gi")),
		},
	}
}

func artifactPodName(id string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	base := "zeta-artifact-" + id
	if len(base) > 54 {
		base = base[:54]
	}
	return fmt.Sprintf("%s-%x", base, b)
}

func jobName(profile string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "zeta-" + slug(profile) + "-000000"
	}
	return fmt.Sprintf("zeta-%s-%x", slug(profile), b)
}

func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "build"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func lockedContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptrB(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func quantity(s string) resource.Quantity { return resource.MustParse(s) }
func quantityPtr(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
func ptr(s string) *string  { return &s }
func ptrB(b bool) *bool     { return &b }
func ptrI64(i int64) *int64 { return &i }
func ptrI32(i int32) *int32 { return &i }
