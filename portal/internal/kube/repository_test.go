package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"zeta/portal/internal/domain"
)

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"main":            "'main'",
		"git://host/repo": "'git://host/repo'",
		"a'b":             "'a'\\''b'",
		"$(rm -rf /)":     "'$(rm -rf /)'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHelperImage(t *testing.T) {
	cases := []struct {
		name     string
		repo     Repository
		image    string
		tag      string
		expected string
	}{
		{"defaults", Repository{}, "zeta/portal", "0.1", "zeta/portal:0.1"},
		{"tag only", Repository{imageTag: "0.2"}, "zeta/portal", "0.1", "zeta/portal:0.2"},
		{"registry and tag", Repository{imageRegistry: "ghcr.io/owner", imageTag: "0.1.0"}, "zeta/portal", "0.1", "ghcr.io/owner/zeta/portal:0.1.0"},
		{"trailing slash", Repository{imageRegistry: "ghcr.io/owner/"}, "zeta/git-server", "0.1", "ghcr.io/owner/zeta/git-server:0.1"},
	}
	for _, tc := range cases {
		if got := tc.repo.helperImage(tc.image, tc.tag); got != tc.expected {
			t.Errorf("%s: helperImage() = %q, want %q", tc.name, got, tc.expected)
		}
	}
}

func TestProfileEnvSorted(t *testing.T) {
	profile := domain.Profile{Env: map[string]string{"ZED": "1", "ALPHA": "2"}}
	env := profileEnv(profile)
	if len(env) != 2 || env[0].Name != "ALPHA" || env[1].Name != "ZED" {
		t.Fatalf("profileEnv() = %+v", env)
	}
}

func TestProfileEnvFromSecretsOptional(t *testing.T) {
	profile := domain.Profile{EnvFromSecrets: []string{"zeta-build-secrets"}}
	envFrom := profileEnvFrom(profile)
	if len(envFrom) != 1 || envFrom[0].SecretRef == nil {
		t.Fatalf("profileEnvFrom() = %+v", envFrom)
	}
	if envFrom[0].SecretRef.Optional == nil || !*envFrom[0].SecretRef.Optional {
		t.Fatal("secret env source should be optional")
	}
}

func TestVolumeRole(t *testing.T) {
	cases := map[string]string{
		artifactsPVC: "artifacts",
		cachePVC:     "cache",
		contextPVC:   "context",
		"custom":     "custom",
	}
	for name, want := range cases {
		if got := volumeRole(name); got != want {
			t.Errorf("volumeRole(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestDerefString(t *testing.T) {
	if got := derefString(nil); got != "" {
		t.Fatalf("derefString(nil) = %q", got)
	}
	v := "local-path"
	if got := derefString(&v); got != v {
		t.Fatalf("derefString(&v) = %q", got)
	}
}

func TestEnsurePVCPerProfile(t *testing.T) {
	r := &Repository{core: fake.NewSimpleClientset().CoreV1(), namespace: "test"}
	if err := r.ensurePVC(context.Background(), "go-cache", "3Gi", "cache", "ebs-sc"); err != nil {
		t.Fatal(err)
	}
	pvc, err := r.core.PersistentVolumeClaims("test").Get(context.Background(), "go-cache", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pvc.Labels[managedBy] != managedValue || pvc.Labels[volumeRoleLabel] != "cache" {
		t.Fatalf("labels = %+v", pvc.Labels)
	}
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse("3Gi")) != 0 {
		t.Fatalf("size = %s", got.String())
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "ebs-sc" {
		t.Fatalf("storageClass = %v", pvc.Spec.StorageClassName)
	}
}

func TestListVolumes(t *testing.T) {
	used := time.Now().UTC().Truncate(time.Second)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "go-cache",
			Namespace: "test",
			Labels:    map[string]string{managedBy: managedValue, volumeRoleLabel: "cache"},
			Annotations: map[string]string{
				lastUsedAnnotation:    used.Format(time.RFC3339),
				lastBuildAnnotation:   "zeta-go-1",
				lastProfileAnnotation: "go",
				lastOwnerAnnotation:   "dev@example.com",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("3Gi")}},
		},
	}
	r := &Repository{core: fake.NewSimpleClientset(pvc).CoreV1(), namespace: "test"}
	volumes, err := r.ListVolumes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 1 {
		t.Fatalf("volumes = %+v", volumes)
	}
	v := volumes[0]
	if v.Name != "go-cache" || v.Role != "cache" || v.CapacityBytes != 3*1024*1024*1024 {
		t.Fatalf("volume = %+v", v)
	}
	if v.LastUsedAt == nil || !v.LastUsedAt.Equal(used) || v.LastBuild != "zeta-go-1" || v.LastProfile != "go" || v.LastOwner != "dev@example.com" {
		t.Fatalf("volume metadata = %+v", v)
	}
}

func TestCreateUsesProfileVolumes(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := &Repository{core: cs.CoreV1(), batch: cs.BatchV1(), namespace: "test"}
	profile := domain.Profile{
		Name:          "go",
		Language:      "go",
		Image:         "zeta/go:0.1",
		WorkDir:       "/workspace",
		ArtifactPath:  "/artifacts/go",
		CachePVC:      "go-cache",
		CacheSize:     "3Gi",
		ArtifactsPVC:  "go-artifacts",
		ArtifactsSize: "2Gi",
		Commands:      []string{"true"},
	}
	id, err := r.Create(context.Background(), domain.BuildSpec{Repo: "git://example/repo.git", Profile: profile, Owner: "dev@example.com", SyncToken: "sync-secret"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := cs.BatchV1().Jobs("test").Get(context.Background(), id, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if job.Annotations["zeta.io/cache-pvc"] != "go-cache" || job.Annotations["zeta.io/artifacts-pvc"] != "go-artifacts" {
		t.Fatalf("job annotations = %+v", job.Annotations)
	}
	owner, err := r.BuildOwner(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if owner != "dev@example.com" {
		t.Fatalf("owner = %q", owner)
	}
	claims := map[string]string{}
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			claims[v.Name] = v.PersistentVolumeClaim.ClaimName
		}
	}
	if claims["cache"] != "go-cache" || claims["artifacts"] != "go-artifacts" {
		t.Fatalf("job volumes = %+v", claims)
	}
	syncEnv := map[string]string{}
	for _, c := range job.Spec.Template.Spec.Containers {
		if c.Name == "syncd" {
			for _, env := range c.Env {
				syncEnv[env.Name] = env.Value
			}
		}
	}
	if syncEnv["ZETA_SYNC_TOKEN"] != "sync-secret" {
		t.Fatalf("sync token env = %q", syncEnv["ZETA_SYNC_TOKEN"])
	}
	if strings.Contains(syncEnv["ZETA_SYNC_URL"], "token=") {
		t.Fatalf("sync token leaked into URL: %q", syncEnv["ZETA_SYNC_URL"])
	}
	for name, size := range map[string]string{"go-cache": "3Gi", "go-artifacts": "2Gi"} {
		pvc, err := cs.CoreV1().PersistentVolumeClaims("test").Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse(size)) != 0 {
			t.Fatalf("%s size = %s", name, got.String())
		}
		if pvc.Annotations[lastBuildAnnotation] != id || pvc.Annotations[lastOwnerAnnotation] != "dev@example.com" {
			t.Fatalf("%s annotations = %+v", name, pvc.Annotations)
		}
		wantRole := "cache"
		if name == "go-artifacts" {
			wantRole = "artifacts"
		}
		if pvc.Labels[volumeRoleLabel] != wantRole {
			t.Fatalf("%s role = %q, want %q", name, pvc.Labels[volumeRoleLabel], wantRole)
		}
	}
}

func testPVC(name, size string) *corev1.PersistentVolumeClaim {
	storageClass := "local-path"
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test",
			Labels:    map[string]string{managedBy: managedValue, volumeRoleLabel: "cache"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &storageClass,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)}},
		},
	}
}

func testSC(name string, expandable bool) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: name},
		Provisioner:          "test",
		AllowVolumeExpansion: &expandable,
	}
}

func TestVolumeQuota(t *testing.T) {
	cs := fake.NewSimpleClientset(testPVC("cache-a", "2Gi"), testPVC("cache-b", "3Gi"))
	r := &Repository{core: cs.CoreV1(), batch: cs.BatchV1(), storage: cs.StorageV1(), namespace: "test", volumeQuotaBytes: 10 << 30, volumeMaxBytes: 5 << 30}
	quota, err := r.VolumeQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quota.UsedBytes != 5<<30 || quota.RemainingBytes != 5<<30 || quota.MaxVolumeBytes != 5<<30 {
		t.Fatalf("quota = %+v", quota)
	}
}

func TestResizeVolume(t *testing.T) {
	cs := fake.NewSimpleClientset(testPVC("cache-a", "1Gi"), testSC("local-path", true))
	r := &Repository{core: cs.CoreV1(), batch: cs.BatchV1(), storage: cs.StorageV1(), namespace: "test", volumeQuotaBytes: 10 << 30, volumeMaxBytes: 5 << 30}
	if err := r.ResizeVolume(context.Background(), "cache-a", "3Gi"); err != nil {
		t.Fatal(err)
	}
	pvc, err := cs.CoreV1().PersistentVolumeClaims("test").Get(context.Background(), "cache-a", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.Cmp(resource.MustParse("3Gi")) != 0 {
		t.Fatalf("size = %s", got.String())
	}
	if err := r.ResizeVolume(context.Background(), "cache-a", "6Gi"); err == nil {
		t.Fatal("expected max volume error")
	}
	if err := r.ResizeVolume(context.Background(), "cache-a", "2Gi"); err == nil {
		t.Fatal("expected shrink error")
	}
}

func TestResizeVolumeRequiresExpansion(t *testing.T) {
	cs := fake.NewSimpleClientset(testPVC("cache-a", "1Gi"), testSC("local-path", false))
	r := &Repository{core: cs.CoreV1(), batch: cs.BatchV1(), storage: cs.StorageV1(), namespace: "test"}
	if err := r.ResizeVolume(context.Background(), "cache-a", "2Gi"); err == nil {
		t.Fatal("expected expansion error")
	}
}

func TestDeleteVolume(t *testing.T) {
	cs := fake.NewSimpleClientset(testPVC("cache-a", "1Gi"))
	r := &Repository{core: cs.CoreV1(), batch: cs.BatchV1(), storage: cs.StorageV1(), namespace: "test"}
	if err := r.DeleteVolume(context.Background(), "cache-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("test").Get(context.Background(), "cache-a", metav1.GetOptions{}); !apierrs.IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestArtifactTarCommand(t *testing.T) {
	got := artifactTarCommand("/artifacts/go", []string{"*.log", "coverage/"})
	want := "tar -C '/artifacts/go' --exclude='*.log' --exclude='coverage/' -czf - . | base64"
	if got != want {
		t.Fatalf("artifactTarCommand() = %q, want %q", got, want)
	}
}

func TestEnsureEnvironment(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := &Repository{core: cs.CoreV1(), apps: cs.AppsV1(), namespace: "test", storageClass: "local-path"}
	profile := domain.Profile{Name: "go", Image: "zeta/go:0.1", WorkDir: "/workspace", CachePVC: "shared-cache"}
	env, err := r.EnsureEnvironment(context.Background(), domain.EnvSpec{ID: "My Project", Profile: profile, Owner: "alice@example.com", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if env.ID != "my-project" || env.Profile != "go" || env.Owner != "alice@example.com" || env.Active {
		t.Fatalf("unexpected env: %+v", env)
	}
	wantSize := quantity(defaultEnvSize)
	if env.StorageClass != "local-path" || env.SizeBytes != wantSize.Value() {
		t.Fatalf("unexpected env size/class: %+v", env)
	}
	pvc, err := cs.CoreV1().PersistentVolumeClaims("test").Get(context.Background(), envPVCName("my-project"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pvc.Labels[volumeRoleLabel] != "env" || pvc.Labels[envLabel] != "my-project" {
		t.Fatalf("unexpected pvc labels: %+v", pvc.Labels)
	}
	secret, err := cs.CoreV1().Secrets("test").Get(context.Background(), envSecretName("my-project"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(secret.Data[envTokenKey]) != "tok" {
		t.Fatalf("unexpected token: %q", string(secret.Data[envTokenKey]))
	}
	dep, err := cs.AppsV1().Deployments("test").Get(context.Background(), envDeployName("my-project"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		t.Fatalf("expected replicas 0, got %v", dep.Spec.Replicas)
	}
	if dep.Annotations[ownerAnnotation] != "alice@example.com" {
		t.Fatalf("unexpected owner annotation: %+v", dep.Annotations)
	}
	owner, err := r.EnvironmentOwner(context.Background(), "my-project")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "alice@example.com" {
		t.Fatalf("unexpected owner: %q", owner)
	}
	if len(dep.Spec.Template.Spec.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(dep.Spec.Template.Spec.Containers))
	}
	syncd := dep.Spec.Template.Spec.Containers[1]
	envVars := map[string]string{}
	for _, item := range syncd.Env {
		envVars[item.Name] = item.Value
	}
	if envVars["ZETA_SYNC_URL"] != "ws://zeta-portal/api/sync/my-project/agent" {
		t.Fatalf("unexpected sync URL: %q", envVars["ZETA_SYNC_URL"])
	}
	if strings.Contains(envVars["ZETA_SYNC_URL"], "token=") {
		t.Fatalf("sync token leaked into URL: %q", envVars["ZETA_SYNC_URL"])
	}
}

func TestEnsureEnvironmentKeepsToken(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := &Repository{core: cs.CoreV1(), apps: cs.AppsV1(), namespace: "test", storageClass: "local-path"}
	profile := domain.Profile{Name: "go", Image: "zeta/go:0.1"}
	if _, err := r.EnsureEnvironment(context.Background(), domain.EnvSpec{ID: "dev", Profile: profile, Token: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EnsureEnvironment(context.Background(), domain.EnvSpec{ID: "dev", Profile: profile, Token: "second"}); err != nil {
		t.Fatal(err)
	}
	token, err := r.EnvironmentToken(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if token != "first" {
		t.Fatalf("token = %q, want first", token)
	}
}

func TestSetEnvironmentActive(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := &Repository{core: cs.CoreV1(), apps: cs.AppsV1(), namespace: "test", storageClass: "local-path"}
	profile := domain.Profile{Name: "go", Image: "zeta/go:0.1"}
	if _, err := r.EnsureEnvironment(context.Background(), domain.EnvSpec{ID: "dev", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetEnvironmentActive(context.Background(), "dev", true); err != nil {
		t.Fatal(err)
	}
	dep, _ := cs.AppsV1().Deployments("test").Get(context.Background(), envDeployName("dev"), metav1.GetOptions{})
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Fatalf("expected replicas 1, got %v", dep.Spec.Replicas)
	}
	if err := r.SetEnvironmentActive(context.Background(), "dev", false); err != nil {
		t.Fatal(err)
	}
	dep, _ = cs.AppsV1().Deployments("test").Get(context.Background(), envDeployName("dev"), metav1.GetOptions{})
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		t.Fatalf("expected replicas 0, got %v", dep.Spec.Replicas)
	}
}

func TestListAndDeleteEnvironment(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := &Repository{core: cs.CoreV1(), apps: cs.AppsV1(), namespace: "test", storageClass: "local-path"}
	profile := domain.Profile{Name: "go", Image: "zeta/go:0.1"}
	for _, id := range []string{"alpha", "beta"} {
		if _, err := r.EnsureEnvironment(context.Background(), domain.EnvSpec{ID: id, Profile: profile}); err != nil {
			t.Fatal(err)
		}
	}
	envs, err := r.ListEnvironments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 || envs[0].ID != "alpha" || envs[1].ID != "beta" {
		t.Fatalf("unexpected envs: %+v", envs)
	}
	if err := r.DeleteEnvironment(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AppsV1().Deployments("test").Get(context.Background(), envDeployName("alpha"), metav1.GetOptions{}); !apierrs.IsNotFound(err) {
		t.Fatalf("expected deployment gone, got %v", err)
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("test").Get(context.Background(), envPVCName("alpha"), metav1.GetOptions{}); !apierrs.IsNotFound(err) {
		t.Fatalf("expected pvc gone, got %v", err)
	}
}

func TestEnsureEnvironmentAddsIDE(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := &Repository{core: cs.CoreV1(), apps: cs.AppsV1(), namespace: "test", storageClass: "local-path", ideImage: "codercom/code-server:4.143.1"}
	profile := domain.Profile{Name: "go", Image: "zeta/go:0.1", WorkDir: "/workspace", CachePVC: "shared-cache"}
	if _, err := r.EnsureEnvironment(context.Background(), domain.EnvSpec{ID: "dev", Profile: profile, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	dep, err := cs.AppsV1().Deployments("test").Get(context.Background(), envDeployName("dev"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(dep.Spec.Template.Spec.Containers) != 3 {
		t.Fatalf("expected 3 containers, got %d", len(dep.Spec.Template.Spec.Containers))
	}
	ide := dep.Spec.Template.Spec.Containers[2]
	if ide.Name != ideContainerName || ide.Image != "codercom/code-server:4.143.1" {
		t.Fatalf("unexpected IDE container: %+v", ide)
	}
	foundWorkspace := false
	for _, mount := range ide.VolumeMounts {
		if mount.Name == "workspace" && mount.MountPath == "/workspace" {
			foundWorkspace = true
		}
	}
	if !foundWorkspace {
		t.Fatal("IDE container does not mount the workspace")
	}
}

func TestEnvIDEPodSkipsTerminatingPod(t *testing.T) {
	now := metav1.Now()
	pod := func(name string, deleting bool) *corev1.Pod {
		meta := metav1.ObjectMeta{Name: name, Namespace: "test", Labels: map[string]string{envLabel: "dev"}}
		if deleting {
			meta.DeletionTimestamp = &now
		}
		return &corev1.Pod{
			ObjectMeta: meta,
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: envContainerName, Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
					{Name: ideContainerName, Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				},
			},
		}
	}
	cs := fake.NewSimpleClientset(pod("old", true), pod("new", false))
	r := &Repository{core: cs.CoreV1(), namespace: "test"}
	got, err := r.envIDEPod(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if got != "new" {
		t.Fatalf("envIDEPod() = %q, want new", got)
	}
}

func TestEnsureEnvironmentIDEAddsMissingSidecar(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := &Repository{core: cs.CoreV1(), apps: cs.AppsV1(), namespace: "test", storageClass: "local-path"}
	profile := domain.Profile{Name: "go", Image: "zeta/go:0.1"}
	if _, err := r.EnsureEnvironment(context.Background(), domain.EnvSpec{ID: "dev", Profile: profile, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	r.ideImage = "codercom/code-server:4.143.1"
	if err := r.EnsureEnvironmentIDE(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	dep, err := cs.AppsV1().Deployments("test").Get(context.Background(), envDeployName("dev"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(dep.Spec.Template.Spec.Containers) != 3 {
		t.Fatalf("expected 3 containers after enabling IDE, got %d", len(dep.Spec.Template.Spec.Containers))
	}
}

func TestPodProblem(t *testing.T) {
	mount := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "zeta-env-x"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "env",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "FailedMount", Message: "mount failed"}},
			}},
		},
	}
	msg, definitive := podProblem(mount)
	if msg == "" || definitive {
		t.Fatalf("mount problem = %q definitive=%v, want message and non-definitive", msg, definitive)
	}
	if !strings.Contains(msg, "mount") {
		t.Fatalf("mount problem message = %q", msg)
	}

	image := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "zeta-env-y"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "env",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "not found"}},
			}},
		},
	}
	if _, definitive := podProblem(image); !definitive {
		t.Fatal("image pull failure should be definitive")
	}

	unsched := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "zeta-env-z"},
		Status: corev1.PodStatus{
			Phase:      corev1.PodPending,
			Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Message: "no nodes"}},
		},
	}
	if _, definitive := podProblem(unsched); !definitive {
		t.Fatal("unschedulable pod should be definitive")
	}
}
