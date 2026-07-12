package policy

import (
	"testing"
	"time"
)

func TestCompileConfigs_Empty(t *testing.T) {
	p, err := CompileConfigs(nil)
	if err != nil {
		t.Fatalf("CompileConfigs(nil): %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil, got %v", p)
	}
}

func TestCompileConfigs_InvalidRegex(t *testing.T) {
	_, err := CompileConfigs([]Config{{
		Name:   "bad",
		Match:  MatchDef{Tool: "*", Inputs: map[string]string{"branch": "[invalid"}},
		Action: "require_approval",
	}})
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestPolicy_MatchToolGlob(t *testing.T) {
	policies, err := CompileConfigs([]Config{{
		Name:   "protect-git-push",
		Match:  MatchDef{Tool: "git.push"},
		Action: "require_approval",
	}})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		tool    string
		matched bool
	}{
		{"git.push", true},
		{"git.commit", false},
		{"read", false},
		{"git.status", false},
	}
	for _, tc := range tests {
		got := policies[0].Matches(tc.tool, nil)
		if got != tc.matched {
			t.Errorf("Matches(%q) = %v, want %v", tc.tool, got, tc.matched)
		}
	}
}

func TestPolicy_WildcardGlob(t *testing.T) {
	policies, err := CompileConfigs([]Config{{
		Name:   "any-git",
		Match:  MatchDef{Tool: "git.*"},
		Action: "require_approval",
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range []string{"git.push", "git.commit", "git.merge", "git.rebase"} {
		if !policies[0].Matches(tool, nil) {
			t.Errorf("Matches(%q) = false, want true", tool)
		}
	}
	if policies[0].Matches("read", nil) {
		t.Error("Matches(read) = true, want false")
	}
}

func TestPolicy_CatchAll(t *testing.T) {
	policies, err := CompileConfigs([]Config{{
		Name:   "catch-all",
		Match:  MatchDef{Tool: "*"},
		Action: "require_approval",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !policies[0].Matches("anything", nil) {
		t.Error("catch-all should match any tool")
	}
}

func TestPolicy_InputFieldMatch(t *testing.T) {
	policies, err := CompileConfigs([]Config{{
		Name:   "protect-main",
		Match:  MatchDef{Tool: "git.push", Inputs: map[string]string{"branch": "main"}},
		Action: "require_approval",
	}})
	if err != nil {
		t.Fatal(err)
	}

	// Tool matches AND branch=main
	if !policies[0].Matches("git.push", map[string]any{"branch": "main"}) {
		t.Error("should match: git.push with branch=main")
	}
	// Tool matches but branch != main
	if policies[0].Matches("git.push", map[string]any{"branch": "develop"}) {
		t.Error("should not match: git.push with branch=develop")
	}
	// Branch field missing
	if policies[0].Matches("git.push", map[string]any{}) {
		t.Error("should not match: missing branch field")
	}
	// Different tool
	if policies[0].Matches("read", map[string]any{"branch": "main"}) {
		t.Error("should not match: wrong tool")
	}
}

func TestPolicy_InputRegex(t *testing.T) {
	policies, err := CompileConfigs([]Config{{
		Name:   "protect-prod",
		Match:  MatchDef{Tool: "deploy", Inputs: map[string]string{"env": "prod(uction)?"}},
		Action: "require_approval",
	}})
	if err != nil {
		t.Fatal(err)
	}

	if !policies[0].Matches("deploy", map[string]any{"env": "prod"}) {
		t.Error("should match: deploy env=prod")
	}
	if !policies[0].Matches("deploy", map[string]any{"env": "production"}) {
		t.Error("should match: deploy env=production")
	}
	if policies[0].Matches("deploy", map[string]any{"env": "staging"}) {
		t.Error("should not match: deploy env=staging")
	}
}

func TestDefaultTimeout(t *testing.T) {
	p := Policy{Config: Config{Timeout: 30 * time.Second}}
	if p.DefaultTimeout() != 30*time.Second {
		t.Errorf("expected 30s, got %v", p.DefaultTimeout())
	}

	p2 := Policy{Config: Config{Timeout: 0}}
	if p2.DefaultTimeout() != 5*time.Minute {
		t.Errorf("expected 5m default, got %v", p2.DefaultTimeout())
	}
}

func TestMultiplePolicies(t *testing.T) {
	policies, err := CompileConfigs([]Config{
		{Name: "git", Match: MatchDef{Tool: "git.*"}, Action: "require_approval"},
		{Name: "deploy", Match: MatchDef{Tool: "deploy"}, Action: "require_approval"},
		{Name: "read", Match: MatchDef{Tool: "read"}, Action: "allow"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 3 {
		t.Fatalf("expected 3 policies, got %d", len(policies))
	}
}
