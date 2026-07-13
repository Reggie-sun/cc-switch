package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRoleProfilesSkipsIncompleteProfiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reviewer.toml"), []byte(`name = "reviewer"
description = "Independent review"
model = "gpt-5.6-sol"
model_reasoning_effort = "high"
developer_instructions = "Review only."
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "invalid.toml"), []byte(`name = "invalid"`), 0o600); err != nil {
		t.Fatal(err)
	}

	roles, err := loadRoleProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0].Name != "reviewer" || roles[0].Model != "gpt-5.6-sol" {
		t.Fatalf("roles = %#v", roles)
	}
}

func TestAgentSetRoleAppliesProfile(t *testing.T) {
	a := &Agent{model: "gpt-5.6-terra", reasoningEffort: "high", mode: "yolo", roles: []roleProfile{{
		Name: "reviewer", Model: "gpt-5.6-sol", ReasoningEffort: "medium", DeveloperInstructions: "Review only.",
	}}}
	if err := a.SetRole("reviewer"); err != nil {
		t.Fatal(err)
	}
	if a.GetRole() != "reviewer" || a.GetModel() != "gpt-5.6-sol" || a.GetReasoningEffort() != "medium" || a.mode != "yolo" {
		t.Fatalf("role=%q model=%q effort=%q mode=%q", a.GetRole(), a.GetModel(), a.GetReasoningEffort(), a.mode)
	}
}
