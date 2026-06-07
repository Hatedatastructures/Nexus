package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExpandEnv_NoExpansionNeeded verifies that plain strings pass through unchanged.
func TestExpandEnv_NoExpansionNeeded(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderConfig{
			"test": {
				APIKey:  "plain-key",
				BaseURL: "https://api.example.com",
			},
		},
	}

	expandEnv(cfg)

	if cfg.Providers["test"].APIKey != "plain-key" {
		t.Errorf("APIKey = %q, want plain-key", cfg.Providers["test"].APIKey)
	}
}

func TestExpandEnvString_Plain(t *testing.T) {
	result := expandEnvString("hello world")
	if result != "hello world" {
		t.Errorf("expandEnvString('hello world') = %q, want 'hello world'", result)
	}
}

func TestExpandEnvString_WithVar(t *testing.T) {
	t.Setenv("MY_VAR", "value123")
	result := expandEnvString("prefix-${MY_VAR}-suffix")
	if result != "prefix-value123-suffix" {
		t.Errorf("expandEnvString = %q, want prefix-value123-suffix", result)
	}
}

func TestLoad_WithEnvExpansion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	t.Setenv("EXPAND_TEST_KEY", "expanded-key-999")
	t.Setenv("EXPAND_TEST_URL", "https://expanded.example.com")

	content := `
agent:
  proxy: "${EXPAND_TEST_URL}"
providers:
  test:
    api_key: "${EXPAND_TEST_KEY}"
    base_url: "${EXPAND_TEST_URL}"
    oauth_url: "${EXPAND_TEST_URL}"
gateway:
  platforms:
    - platform: telegram
      token: "${EXPAND_TEST_KEY}"
`
	if wErr := os.WriteFile(path, []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load with env expansion: %v", err)
	}

	if cfg.Providers["test"].APIKey != "expanded-key-999" {
		t.Errorf("Provider APIKey = %q, want expanded", cfg.Providers["test"].APIKey)
	}
	if cfg.Providers["test"].BaseURL != "https://expanded.example.com" {
		t.Errorf("Provider BaseURL = %q", cfg.Providers["test"].BaseURL)
	}
	if cfg.Providers["test"].OAuthURL != "https://expanded.example.com" {
		t.Errorf("Provider OAuthURL = %q", cfg.Providers["test"].OAuthURL)
	}
	if cfg.Agent.Proxy != "https://expanded.example.com" {
		t.Errorf("Agent.Proxy = %q", cfg.Agent.Proxy)
	}
	if len(cfg.Gateway.Platforms) > 0 && cfg.Gateway.Platforms[0].Token != "expanded-key-999" {
		t.Errorf("Platform Token = %q, want expanded", cfg.Gateway.Platforms[0].Token)
	}
}

func TestExpandEnv_FallbackChainWithEnvExpansion(t *testing.T) {
	t.Setenv("FB_CHAIN_KEY", "chain-env-key")
	t.Setenv("FB_CHAIN_URL", "https://chain.example.com")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
agent:
  fallback_chain:
    - provider: fb
      model: m1
      api_key: "${FB_CHAIN_KEY}"
      base_url: "${FB_CHAIN_URL}"
  fallback_model_cfg:
    provider: legacy
    model: old
    api_key: "${FB_CHAIN_KEY}"
    base_url: "${FB_CHAIN_URL}"
`
	if wErr := os.WriteFile(path, []byte(content), 0644); wErr != nil {
		t.Fatal(wErr)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agent.FallbackChain[0].APIKey != "chain-env-key" {
		t.Errorf("FallbackChain APIKey = %q, want chain-env-key", cfg.Agent.FallbackChain[0].APIKey)
	}
	if cfg.Agent.FallbackChain[0].BaseURL != "https://chain.example.com" {
		t.Errorf("FallbackChain BaseURL = %q", cfg.Agent.FallbackChain[0].BaseURL)
	}
	if cfg.Agent.FallbackModelCfg.APIKey != "chain-env-key" {
		t.Errorf("FallbackModelCfg APIKey = %q", cfg.Agent.FallbackModelCfg.APIKey)
	}
	if cfg.Agent.FallbackModelCfg.BaseURL != "https://chain.example.com" {
		t.Errorf("FallbackModelCfg BaseURL = %q", cfg.Agent.FallbackModelCfg.BaseURL)
	}
}
