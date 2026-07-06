package hub

import "testing"

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
