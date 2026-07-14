package core

import (
	"strings"
	"testing"
)

type stubRoleAgent struct {
	stubAgent
	roles []AgentRole
	role  string
}

func (a *stubRoleAgent) SetRole(name string) error {
	a.role = name
	return nil
}

func (a *stubRoleAgent) GetRole() string { return a.role }

func (a *stubRoleAgent) AvailableRoles() []AgentRole {
	return append([]AgentRole(nil), a.roles...)
}

func TestHandleCommand_RoleIsBuiltIn(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	agent := &stubRoleAgent{roles: []AgentRole{{Name: "reviewer", Description: "Reviews changes"}}}
	e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)

	handled := e.handleCommand(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}, "/role")

	if !handled {
		t.Fatal("/role was not handled as a built-in command")
	}
	sent := p.getSent()
	if len(sent) != 1 || !strings.Contains(sent[0], "Available roles:") || !strings.Contains(sent[0], "reviewer") {
		t.Fatalf("/role reply = %#v, want role list", sent)
	}
}
