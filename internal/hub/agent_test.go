package hub

import (
	"strings"
	"testing"
)

func TestAgentSpriteName(t *testing.T) {
	cases := map[string]string{
		"seekinggradient/kauai-2026": "afs-seekinggradient-kauai-2026",
		"Alice/My Repo":              "afs-alice-my-repo",
		"a/b.c":                      "afs-a-b-c",
	}
	for in, want := range cases {
		user, repo := "", ""
		for i := 0; i < len(in); i++ {
			if in[i] == '/' {
				user, repo = in[:i], in[i+1:]
				break
			}
		}
		if got := agentSpriteName(user, repo); got != want {
			t.Errorf("agentSpriteName(%q,%q) = %q, want %q", user, repo, got, want)
		}
	}
}

func TestAgentEnabledNilSafe(t *testing.T) {
	var m *AgentManager
	if m.Enabled() {
		t.Fatal("nil AgentManager should be disabled")
	}
	m = NewAgentManager("", "", "", "", nil, nil)
	if m.Enabled() {
		t.Fatal("unconfigured AgentManager should be disabled")
	}
}

func TestRepoServiceEnvUsesHubProxyWithoutOperatorKey(t *testing.T) {
	m := NewAgentManager("sprites-token", "operator-openai-key", "test-model", "https://hub.example", nil, nil)
	got := m.repoServiceEnv("my-repo", "afs-user-pat", ",AFS_BIN=/home/sprite/.local/bin/afs")

	for _, want := range []string{
		"AGENTSFS_ROOT=/home/sprite/wiki",
		"AGENTSFS_NAME=my-repo",
		"CHAT_MODEL=test-model",
		"AGENTSFS_LLM_BASE_URL=https://hub.example/v1/agent-llm",
		"AGENTSFS_LLM_KEY=afs-user-pat",
		"AFS_BIN=/home/sprite/.local/bin/afs",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("service env missing %q", want)
		}
	}
	if strings.Contains(got, "OPENAI_API_KEY") || strings.Contains(got, m.OpenAIKey) {
		t.Fatal("legacy repository Sprite service env exposes the operator OpenAI key")
	}
}
