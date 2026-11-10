package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

type Config struct {
	Addr             string
	Namespace        string
	KubeConfig       string
	MasterURL        string
	ProfilesFile     string
	ImageRegistry    string
	ImageTag         string
	StorageClass     string
	VolumeQuotaBytes int64
	VolumeMaxBytes   int64
	EnvIdleTimeout   time.Duration
	Auth             AuthConfig
}

type AuthConfig struct {
	OIDCIssuer           string
	OIDCDiscoveryURL     string
	OIDCAuthorizationURL string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCRedirectURL      string
	OIDCScopes           string
	OIDCAllowedGroups    string
	OIDCAllowedUsers     string
	OIDCUsernameClaim    string
	OIDCEmailClaim       string
	OIDCGroupsClaim      string
	OIDCSessionKey       string
	OIDCSessionTTL       string
	OIDCDev              bool
	SecureCookie         bool
}

func Load(args []string) (Config, error) {
	cfg := Config{
		Addr:          env("ZETA_ADDR", ":8080"),
		Namespace:     env("ZETA_NAMESPACE", "zeta-poc"),
		KubeConfig:    env("ZETA_KUBECONFIG", ""),
		MasterURL:     env("ZETA_MASTER", ""),
		ProfilesFile:  env("ZETA_PROFILES_FILE", ""),
		ImageRegistry: env("ZETA_IMAGE_REGISTRY", ""),
		ImageTag:      env("ZETA_IMAGE_TAG", ""),
		StorageClass:  env("ZETA_STORAGE_CLASS", "local-path"),
		Auth: AuthConfig{
			OIDCIssuer:           env("ZETA_OIDC_ISSUER", ""),
			OIDCDiscoveryURL:     env("ZETA_OIDC_DISCOVERY_URL", ""),
			OIDCAuthorizationURL: env("ZETA_OIDC_AUTHORIZATION_URL", ""),
			OIDCClientID:         env("ZETA_OIDC_CLIENT_ID", "zeta-portal"),
			OIDCClientSecret:     env("ZETA_OIDC_CLIENT_SECRET", ""),
			OIDCRedirectURL:      env("ZETA_OIDC_REDIRECT_URL", ""),
			OIDCScopes:           env("ZETA_OIDC_SCOPES", "openid,profile,email,groups"),
			OIDCAllowedGroups:    env("ZETA_OIDC_ALLOWED_GROUPS", ""),
			OIDCAllowedUsers:     env("ZETA_OIDC_ALLOWED_USERS", ""),
			OIDCUsernameClaim:    env("ZETA_OIDC_USERNAME_CLAIM", "preferred_username"),
			OIDCEmailClaim:       env("ZETA_OIDC_EMAIL_CLAIM", "email"),
			OIDCGroupsClaim:      env("ZETA_OIDC_GROUPS_CLAIM", "groups"),
			OIDCSessionKey:       env("ZETA_OIDC_SESSION_KEY", ""),
			OIDCSessionTTL:       env("ZETA_OIDC_SESSION_TTL", "12h"),
			OIDCDev:              envBool("ZETA_OIDC_DEV", false),
			SecureCookie:         envBool("ZETA_SECURE_COOKIE", false),
		},
	}

	volumeQuota := env("ZETA_VOLUME_QUOTA", "20Gi")
	volumeMax := env("ZETA_VOLUME_MAX", "10Gi")
	envIdle := env("ZETA_ENV_IDLE_TIMEOUT", "15m")

	fs := flag.NewFlagSet("zeta", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "portal listen address")
	fs.StringVar(&cfg.Namespace, "namespace", cfg.Namespace, "Kubernetes namespace for builds")
	fs.StringVar(&cfg.KubeConfig, "kubeconfig", cfg.KubeConfig, "kubeconfig path (dev, empty = in-cluster)")
	fs.StringVar(&cfg.MasterURL, "master", cfg.MasterURL, "Kubernetes master URL (overrides kubeconfig)")
	fs.StringVar(&cfg.ProfilesFile, "profiles-file", cfg.ProfilesFile, "build profiles YAML file (optional)")
	fs.StringVar(&cfg.ImageRegistry, "image-registry", cfg.ImageRegistry, "registry prefix for profile and helper images (e.g. ghcr.io/owner)")
	fs.StringVar(&cfg.ImageTag, "image-tag", cfg.ImageTag, "tag override for profile and helper images")
	fs.StringVar(&cfg.StorageClass, "storage-class", cfg.StorageClass, "StorageClass for shared PVCs")
	fs.StringVar(&volumeQuota, "volume-quota", volumeQuota, "Zeta total volume quota")
	fs.StringVar(&volumeMax, "volume-max", volumeMax, "maximum volume size in Zeta")
	fs.StringVar(&envIdle, "env-idle-timeout", envIdle, "scale environments to zero after this idle duration (0 disables)")
	fs.StringVar(&cfg.Auth.OIDCIssuer, "oidc-issuer", cfg.Auth.OIDCIssuer, "OIDC issuer")
	fs.StringVar(&cfg.Auth.OIDCDiscoveryURL, "oidc-discovery-url", cfg.Auth.OIDCDiscoveryURL, "OIDC discovery URL (default: issuer)")
	fs.StringVar(&cfg.Auth.OIDCAuthorizationURL, "oidc-authorization-url", cfg.Auth.OIDCAuthorizationURL, "OIDC authorization endpoint override")
	fs.StringVar(&cfg.Auth.OIDCClientID, "oidc-client-id", cfg.Auth.OIDCClientID, "OIDC client ID")
	fs.StringVar(&cfg.Auth.OIDCClientSecret, "oidc-client-secret", cfg.Auth.OIDCClientSecret, "OIDC client secret (empty = PKCE public)")
	fs.StringVar(&cfg.Auth.OIDCRedirectURL, "oidc-redirect-url", cfg.Auth.OIDCRedirectURL, "OIDC callback URL")
	fs.StringVar(&cfg.Auth.OIDCScopes, "oidc-scopes", cfg.Auth.OIDCScopes, "OIDC scopes")
	fs.StringVar(&cfg.Auth.OIDCAllowedGroups, "oidc-allowed-groups", cfg.Auth.OIDCAllowedGroups, "comma-separated allowed OIDC groups")
	fs.StringVar(&cfg.Auth.OIDCAllowedUsers, "oidc-allowed-users", cfg.Auth.OIDCAllowedUsers, "comma-separated allowed OIDC usernames, emails, or subjects")
	fs.StringVar(&cfg.Auth.OIDCUsernameClaim, "oidc-username-claim", cfg.Auth.OIDCUsernameClaim, "OIDC username claim")
	fs.StringVar(&cfg.Auth.OIDCEmailClaim, "oidc-email-claim", cfg.Auth.OIDCEmailClaim, "OIDC email claim")
	fs.StringVar(&cfg.Auth.OIDCGroupsClaim, "oidc-groups-claim", cfg.Auth.OIDCGroupsClaim, "OIDC groups claim")
	fs.StringVar(&cfg.Auth.OIDCSessionKey, "oidc-session-key", cfg.Auth.OIDCSessionKey, "session signing key")
	fs.StringVar(&cfg.Auth.OIDCSessionTTL, "oidc-session-ttl", cfg.Auth.OIDCSessionTTL, "session lifetime")
	fs.BoolVar(&cfg.Auth.OIDCDev, "oidc-dev", cfg.Auth.OIDCDev, "dev local auth (not for prod)")
	fs.BoolVar(&cfg.Auth.SecureCookie, "secure-cookie", cfg.Auth.SecureCookie, "Secure cookies (HTTPS)")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if cfg.Auth.OIDCIssuer == "" && !cfg.Auth.OIDCDev {
		return Config{}, fmt.Errorf("--oidc-issuer or --oidc-dev is required (no user kubeconfig)")
	}
	if cfg.Namespace == "" {
		return Config{}, fmt.Errorf("--namespace is required")
	}
	quota, err := resource.ParseQuantity(volumeQuota)
	if err != nil {
		return Config{}, fmt.Errorf("--volume-quota invalid: %w", err)
	}
	maxVolume, err := resource.ParseQuantity(volumeMax)
	if err != nil {
		return Config{}, fmt.Errorf("--volume-max invalid: %w", err)
	}
	cfg.VolumeQuotaBytes = quota.Value()
	cfg.VolumeMaxBytes = maxVolume.Value()
	idle, err := time.ParseDuration(envIdle)
	if err != nil || idle < 0 {
		return Config{}, fmt.Errorf("--env-idle-timeout invalid: %q", envIdle)
	}
	cfg.EnvIdleTimeout = idle
	return cfg, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "yes", "YES":
		return true
	case "0", "false", "FALSE", "no", "NO":
		return false
	default:
		return fallback
	}
}
