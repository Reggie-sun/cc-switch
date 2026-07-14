package core

import "testing"

func TestResolveModelAlias_CaseInsensitive(t *testing.T) {
	models := []ModelOption{{Name: "gpt-5.3-codex", Alias: "Codex"}}

	got := resolveModelAlias(models, "codex")
	if got != "gpt-5.3-codex" {
		t.Fatalf("resolveModelAlias() = %q, want %q", got, "gpt-5.3-codex")
	}
}

func TestResolveModelAlias_NoMatchFallsBackToInput(t *testing.T) {
	models := []ModelOption{{Name: "gpt-5.3-codex", Alias: "codex"}}

	got := resolveModelAlias(models, "gpt-5.4")
	if got != "gpt-5.4" {
		t.Fatalf("resolveModelAlias() = %q, want original input", got)
	}
}

func TestParseModelSwitchArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{name: "legacy syntax", args: []string{"gpt"}, want: "gpt", ok: true},
		{name: "switch syntax", args: []string{"switch", "gpt"}, want: "gpt", ok: true},
		{name: "missing switch target", args: []string{"switch"}, ok: false},
		{name: "unknown subcommand", args: []string{"list", "gpt"}, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseModelSwitchArgs(tt.args)
			if ok != tt.ok {
				t.Fatalf("parseModelSwitchArgs() ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("parseModelSwitchArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRoleName(t *testing.T) {
	roles := []AgentRole{{Name: "reviewer"}, {Name: "code_developer"}}

	for _, tt := range []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "2", want: "code_developer", ok: true},
		{input: "Reviewer", want: "reviewer", ok: true},
		{input: "3", ok: false},
		{input: "missing", ok: false},
	} {
		got, ok := resolveRoleName(roles, tt.input)
		if got != tt.want || ok != tt.ok {
			t.Errorf("resolveRoleName(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}
