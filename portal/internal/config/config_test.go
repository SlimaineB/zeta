package config

import "testing"

func TestLoadRequiresAuthMode(t *testing.T) {
	t.Setenv("ZETA_OIDC_ISSUER", "")
	t.Setenv("ZETA_OIDC_DEV", "false")
	if _, err := Load([]string{"--namespace", "test"}); err == nil {
		t.Fatal("expected error without OIDC issuer or dev mode")
	}
}

func TestLoadDevMode(t *testing.T) {
	cfg, err := Load([]string{"--oidc-dev", "--namespace", "test", "--addr", ":9999"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Namespace != "test" || cfg.Addr != ":9999" || !cfg.Auth.OIDCDev {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadOIDCOptions(t *testing.T) {
	cfg, err := Load([]string{
		"--oidc-issuer", "https://id.example.com",
		"--oidc-discovery-url", "http://dex:5556/dex",
		"--oidc-authorization-url", "https://id.example.com/auth",
		"--oidc-allowed-groups", "devs,platform",
		"--oidc-allowed-users", "alice@example.com",
		"--oidc-username-claim", "login",
		"--oidc-email-claim", "mail",
		"--oidc-groups-claim", "roles",
		"--namespace", "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	auth := cfg.Auth
	if auth.OIDCDiscoveryURL != "http://dex:5556/dex" || auth.OIDCAuthorizationURL != "https://id.example.com/auth" {
		t.Fatalf("OIDC endpoints = %+v", auth)
	}
	if auth.OIDCAllowedGroups != "devs,platform" || auth.OIDCAllowedUsers != "alice@example.com" {
		t.Fatalf("OIDC allowlists = %+v", auth)
	}
	if auth.OIDCUsernameClaim != "login" || auth.OIDCEmailClaim != "mail" || auth.OIDCGroupsClaim != "roles" {
		t.Fatalf("OIDC claims = %+v", auth)
	}
}
