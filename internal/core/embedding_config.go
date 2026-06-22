package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const embeddingConfigFile = "embeddings.env"

// EmbeddingConfigPath is the user-local fallback config read by afs when
// real environment variables are not set. It must never live in an agentsfs
// repo: it can contain API keys.
func EmbeddingConfigPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "agentsfs", embeddingConfigFile), nil
}

func NormalizeEmbeddingProvider(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "openai", "voyage":
		return provider, nil
	default:
		return "", fmt.Errorf("unknown embedding provider %q (want openai or voyage)", provider)
	}
}

func EmbeddingKeyName(provider string) (string, error) {
	provider, err := NormalizeEmbeddingProvider(provider)
	if err != nil {
		return "", err
	}
	if provider == "openai" {
		return "OPENAI_API_KEY", nil
	}
	return "VOYAGE_API_KEY", nil
}

func SaveEmbeddingConfig(provider, key string) (string, error) {
	provider, err := NormalizeEmbeddingProvider(provider)
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("embedding API key is empty")
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", fmt.Errorf("embedding API key must be a single line")
	}
	keyName, err := EmbeddingKeyName(provider)
	if err != nil {
		return "", err
	}
	path, err := EmbeddingConfigPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	body := strings.Join([]string{
		"# Written by `afs embeddings setup`.",
		"# Environment variables set in the shell override these values.",
		"AFS_EMBED_PROVIDER=" + shellQuote(provider),
		keyName + "=" + shellQuote(key),
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, os.Chmod(path, 0o600)
}

func ClearEmbeddingConfig() (string, error) {
	path, err := EmbeddingConfigPath()
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

func loadEmbeddingConfig() (map[string]string, string, error) {
	path, err := EmbeddingConfigPath()
	if err != nil {
		return nil, "", err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]string{}, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	defer file.Close()

	env := map[string]string{}
	scanner := bufio.NewScanner(file)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, path, fmt.Errorf("%s:%d: expected KEY=value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		if !knownEmbeddingEnvKey(key) {
			continue
		}
		env[key] = parseEnvValue(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, path, err
	}
	return env, path, nil
}

func knownEmbeddingEnvKey(key string) bool {
	switch key {
	case "AFS_EMBED_PROVIDER", "AFS_EMBED_MODEL", "AFS_EMBED_URL", "OPENAI_API_KEY", "VOYAGE_API_KEY":
		return true
	default:
		return false
	}
}

func parseEnvValue(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return strings.ReplaceAll(value[1:len(value)-1], `'\''`, `'`)
	}
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return strings.Trim(value, `"`)
	}
	return value
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
