package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddingProviderUsesUserConfigFallback(t *testing.T) {
	t.Setenv("AFS_EMBED_PROVIDER", "")
	t.Setenv("AFS_EMBED_MODEL", "")
	t.Setenv("AFS_EMBED_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("VOYAGE_API_KEY", "")
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	path, err := SaveEmbeddingConfig("openai", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(configHome, "agentsfs", embeddingConfigFile) {
		t.Fatalf("config path = %q", path)
	}

	provider, err := DetectEmbeddingProvider()
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name != "openai" || provider.Model != "text-embedding-3-small" {
		t.Fatalf("provider = %+v", provider)
	}
	if provider.KeyName != "OPENAI_API_KEY" || provider.KeySource != path {
		t.Fatalf("key source not recorded: %+v", provider)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %v, want 0600", info.Mode().Perm())
	}
}

func TestEmbeddingEnvironmentOverridesUserConfig(t *testing.T) {
	t.Setenv("AFS_EMBED_PROVIDER", "openai")
	t.Setenv("AFS_EMBED_MODEL", "")
	t.Setenv("AFS_EMBED_URL", "")
	t.Setenv("OPENAI_API_KEY", "sk-env")
	t.Setenv("VOYAGE_API_KEY", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := SaveEmbeddingConfig("voyage", "pa-config"); err != nil {
		t.Fatal(err)
	}

	provider, err := DetectEmbeddingProvider()
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name != "openai" || provider.KeySource != "environment" {
		t.Fatalf("environment did not override config: %+v", provider)
	}
}

func TestEmbeddingConfigClear(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := SaveEmbeddingConfig("openai", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClearEmbeddingConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config still exists after clear: %v", err)
	}
	if _, err := ClearEmbeddingConfig(); err != nil {
		t.Fatalf("clear should be idempotent: %v", err)
	}
}

func TestEmbeddingConfigRejectsEmptyKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := SaveEmbeddingConfig("openai", " \n "); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty key error, got %v", err)
	}
}
