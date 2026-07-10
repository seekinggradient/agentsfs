package agentbundle

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildIsDeterministicAndExcludesUnwantedFiles(t *testing.T) {
	source := writeFixture(t)
	write := func(name, contents string) {
		t.Helper()
		filename := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".env", "OPENAI_API_KEY=must-not-be-packaged")
	write("README.md", "not needed at runtime")
	write("node_modules/pkg/index.js", "not source")
	write("src/.DS_Store", "not source")
	write("test/leak.test.ts", "not runtime")

	one := filepath.Join(t.TempDir(), "one.tgz")
	two := filepath.Join(t.TempDir(), "two.tgz")
	if err := Build(source, one); err != nil {
		t.Fatal(err)
	}
	if err := Build(source, two); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(one)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(two)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same runtime source produced different archives")
	}
	if err := Validate(bytes.NewReader(a)); err != nil {
		t.Fatalf("generated bundle did not validate: %v", err)
	}
	if bytes.Contains(a, []byte("must-not-be-packaged")) {
		t.Fatal("ignored .env content appeared in compressed bundle")
	}
}

func TestBuildRejectsSecretInAllowedFile(t *testing.T) {
	source := writeFixture(t)
	if err := os.WriteFile(filepath.Join(source, "src", "index.ts"), []byte(`const leaked = "OPENAI_API_KEY=not-a-placeholder-value";`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Build(source, filepath.Join(t.TempDir(), "bundle.tgz"))
	if err == nil {
		t.Fatal("Build accepted a credential-like value in an allowed source file")
	}
}

func TestAllowedPath(t *testing.T) {
	for _, name := range []string{".env", ".env.example", ".git/config", "README.md", "node_modules/x.js", "src/.DS_Store", "src/.env", "test/x.ts", "../src/x.ts", "/src/x.ts"} {
		if AllowedPath(name) {
			t.Errorf("AllowedPath(%q) = true", name)
		}
	}
	for _, name := range []string{"package.json", "package-lock.json", "tsconfig.json", "src/index.ts", "src/web/app.js", "src/web/index.html", "src/web/styles.css"} {
		if !AllowedPath(name) {
			t.Errorf("AllowedPath(%q) = false", name)
		}
	}
}

func writeFixture(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	files := map[string]string{
		"package-lock.json":    `{}`,
		"package.json":         `{}`,
		"tsconfig.json":        `{}`,
		"src/index.ts":         "export {};",
		"src/server/server.ts": "export {};",
		"src/web/app.js":       "export {};",
		"src/web/index.html":   "<!doctype html>",
		"src/web/lib.js":       "export {};",
		"src/web/styles.css":   "body {}",
	}
	for name, contents := range files {
		filename := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return source
}
