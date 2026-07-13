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

func TestAgentManagerUsesDefaultChatModel(t *testing.T) {
	t.Setenv("CHAT_REASONING_EFFORT", "")
	m := NewAgentManager("", "", "", "", nil, nil)
	if m.ChatModel != "gpt-5.6-luna" {
		t.Fatalf("ChatModel = %q, want %q", m.ChatModel, "gpt-5.6-luna")
	}
	if m.ChatReasoningEffort != "high" {
		t.Fatalf("ChatReasoningEffort = %q, want %q", m.ChatReasoningEffort, "high")
	}
}

func TestAgentManagerUsesConfiguredChatReasoningEffort(t *testing.T) {
	t.Setenv("CHAT_REASONING_EFFORT", "medium")
	m := NewAgentManager("", "", "", "", nil, nil)
	if m.ChatReasoningEffort != "medium" {
		t.Fatalf("ChatReasoningEffort = %q, want %q", m.ChatReasoningEffort, "medium")
	}
}

func TestAgentDevURLEnablesWithoutSprites(t *testing.T) {
	m := NewAgentManager("", "", "", "", nil, nil)
	m.DevURL = "http://127.0.0.1:8091"
	if !m.Enabled() {
		t.Fatal("DevURL should enable the agent feature without sprite/OpenAI config")
	}
	url, ready := m.EnsureUser("alice", nil)
	if !ready || url != m.DevURL {
		t.Fatalf("EnsureUser = (%q, %v), want (%q, true) with no provisioning", url, ready, m.DevURL)
	}
}

func TestRepoServiceEnvUsesHubProxyWithoutOperatorKey(t *testing.T) {
	t.Setenv("CHAT_REASONING_EFFORT", "high")
	m := NewAgentManager("sprites-token", "operator-openai-key", "test-model", "https://hub.example", nil, nil)
	got := m.repoServiceEnv("my-repo", "afs-user-pat", ",AFS_BIN=/home/sprite/.local/bin/afs")

	for _, want := range []string{
		"AGENTSFS_ROOT=/home/sprite/wiki",
		"AGENTSFS_NAME=my-repo",
		"CHAT_MODEL=test-model",
		"CHAT_REASONING_EFFORT=high",
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

func TestSharedCloneScriptKeepsOwnerQualifiedHubRemote(t *testing.T) {
	ref := RepoRef{Owner: "alice", Repo: "shared-notes"}
	dir := "/home/sprite/workspace/alice--shared-notes"
	got := cloneRepoScript("dG9rZW4=", "https://hub.example", ref, dir)
	for _, want := range []string{
		"clone https://hub.example/alice/shared-notes.git /home/sprite/workspace/alice--shared-notes",
		"remote add hub https://hub.example/alice/shared-notes.git",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("clone script missing %q: %s", want, got)
		}
	}
}
