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
