package identity

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSubject_ValidTypes(t *testing.T) {
	tests := []struct {
		typ IdentityType
		id  string
	}{
		{IdentityUser, "user-1"},
		{IdentityAgent, "agent-1"},
		{IdentityServiceAccount, "sa-1"},
	}
	for _, tc := range tests {
		sub := Subject{Type: tc.typ, ID: tc.id, DisplayName: "test"}
		if sub.Type != tc.typ || sub.ID != tc.id {
			t.Errorf("expected type=%s id=%s, got type=%s id=%s", tc.typ, tc.id, sub.Type, sub.ID)
		}
	}
}

func TestSubject_Serialization(t *testing.T) {
	sub := Subject{Type: IdentityUser, ID: "u1", DisplayName: "Alice", Metadata: map[string]string{"team": "eng"}}
	data, err := json.Marshal(sub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Subject
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != sub.Type || got.ID != sub.ID || got.DisplayName != sub.DisplayName {
		t.Errorf("round-trip mismatch: %+v vs %+v", sub, got)
	}
	if got.Metadata["team"] != "eng" {
		t.Errorf("expected metadata team=eng, got %q", got.Metadata["team"])
	}
}

func TestUser_Creation(t *testing.T) {
	u := User{ID: "u1", DisplayName: "Bob", SlackTeamID: "T123", PluginID: "slack", CreatedAt: time.Now()}
	if u.ID != "u1" || u.DisplayName != "Bob" {
		t.Errorf("user fields not set correctly: %+v", u)
	}
}

func TestAgent_Creation(t *testing.T) {
	a := Agent{ID: "a1", Name: "opencode", SandboxID: "sbox-1", SessionID: "ses-1", AssignedUserID: "u1"}
	if a.ID != "a1" || a.Name != "opencode" || a.SandboxID != "sbox-1" {
		t.Errorf("agent fields not set correctly: %+v", a)
	}
}

func TestServiceAccount_Creation(t *testing.T) {
	sa := ServiceAccount{ID: "sa-1", Name: "deploy-bot", Description: "Automated deployment"}
	if sa.ID != "sa-1" || sa.Name != "deploy-bot" {
		t.Errorf("service account fields not set correctly: %+v", sa)
	}
}

func TestInstallation_ValidStates(t *testing.T) {
	tests := []struct {
		state InstallationState
		valid bool
	}{
		{InstallationActive, true},
		{InstallationSuspended, true},
		{InstallationDeleted, true},
		{InstallationState("unknown"), false},
	}
	for _, tc := range tests {
		inst := Installation{ID: "i1", Platform: "github", PlatformInstallID: 12345, State: tc.state}
		if inst.State != tc.state {
			t.Errorf("unexpected state: %s", inst.State)
		}
	}
}

func TestInstallation_Serialization(t *testing.T) {
	inst := Installation{
		ID:                "i1",
		Platform:          "github",
		PlatformInstallID: 12345,
		AccountLogin:      "my-org",
		AccountType:       "Organization",
		AccountID:         67890,
		State:             InstallationActive,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	data, err := json.Marshal(inst)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Installation
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != inst.ID || got.PlatformInstallID != inst.PlatformInstallID || got.State != inst.State {
		t.Errorf("round-trip mismatch")
	}
}
