package codex

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/chenhg5/cc-connect/core"
)

type roleProfile = core.AgentRole

func loadRoleProfiles(dir string) ([]roleProfile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	roles := make([]roleProfile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		var raw struct {
			Name                  string `toml:"name"`
			Description           string `toml:"description"`
			Model                 string `toml:"model"`
			ReasoningEffort       string `toml:"model_reasoning_effort"`
			DeveloperInstructions string `toml:"developer_instructions"`
		}
		if _, err := toml.DecodeFile(filepath.Join(dir, entry.Name()), &raw); err != nil {
			continue
		}
		role := roleProfile{Name: raw.Name, Description: raw.Description, Model: raw.Model, ReasoningEffort: raw.ReasoningEffort, DeveloperInstructions: raw.DeveloperInstructions}
		role.Name = strings.TrimSpace(role.Name)
		role.Model = strings.TrimSpace(role.Model)
		role.DeveloperInstructions = strings.TrimSpace(role.DeveloperInstructions)
		if role.Name == "" || role.Model == "" || role.DeveloperInstructions == "" {
			continue
		}
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Name < roles[j].Name })
	return roles, nil
}
