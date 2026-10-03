// Package factory_test verifies role-to-stage mapping and agent resolution (HND-2).
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Domain Service / Policy Verification (Clean Architecture).
//
// Verifies pure business domain mappings for SDLC agent roles without I/O or side effects.
// ==============================================================================
package factory_test

import (
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

// TestRoleForStage_HND2 verifies stage-to-role mappings defined by requirement HND-2:
// 02 -> spec, 03 -> probe, 04 -> coding, 05 -> review, 06 -> coding.
func TestRoleForStage_HND2(t *testing.T) {
	tests := []struct {
		stage    string
		wantRole string
		wantOK   bool
	}{
		{stage: factory.StageIntent, wantRole: "", wantOK: false},
		{stage: "01", wantRole: "", wantOK: false},
		{stage: factory.StageClarificationAndSpec, wantRole: factory.RoleSpec, wantOK: true},
		{stage: "02", wantRole: factory.RoleSpec, wantOK: true},
		{stage: factory.StageFailingProbe, wantRole: factory.RoleProbe, wantOK: true},
		{stage: "03", wantRole: factory.RoleProbe, wantOK: true},
		{stage: factory.StageCoding, wantRole: factory.RoleCoding, wantOK: true},
		{stage: "04", wantRole: factory.RoleCoding, wantOK: true},
		{stage: factory.StageIndependentReview, wantRole: factory.RoleReview, wantOK: true},
		{stage: "05", wantRole: factory.RoleReview, wantOK: true},
		{stage: factory.StageHumanApprovalGate, wantRole: factory.RoleCoding, wantOK: true},
		{stage: "06", wantRole: factory.RoleCoding, wantOK: true},
		{stage: factory.StageDone, wantRole: "", wantOK: false},
		{stage: "07", wantRole: "", wantOK: false},
		{stage: "unknown_stage", wantRole: "", wantOK: false},
	}

	for _, tt := range tests {
		gotRole, gotOK := factory.RoleForStage(tt.stage)
		if gotOK != tt.wantOK {
			t.Errorf("RoleForStage(%q) ok = %v, want %v", tt.stage, gotOK, tt.wantOK)
		}
		if gotRole != tt.wantRole {
			t.Errorf("RoleForStage(%q) role = %q, want %q", tt.stage, gotRole, tt.wantRole)
		}
	}
}

// TestAgentForRole_HND2 verifies agent resolution rules per project configuration:
// - Explicit role mappings returned directly.
// - With agents.probe unset, probe resolves to the coding agent.
func TestAgentForRole_HND2(t *testing.T) {
	// Case 1: Probe explicitly set
	cfgExplicit := factory.ProjectConfig{
		Agents: map[string]string{
			factory.RoleSpec:   "agy",
			factory.RoleProbe:  "opencode",
			factory.RoleCoding: "agy",
			factory.RoleReview: "agy",
		},
	}
	if got := cfgExplicit.AgentForRole(factory.RoleProbe); got != "opencode" {
		t.Errorf("expected explicit probe 'opencode', got %q", got)
	}

	// Case 2: Probe unset, falls back to coding (HND-2)
	cfgFallback := factory.ProjectConfig{
		Agents: map[string]string{
			factory.RoleSpec:   "agy",
			factory.RoleCoding: "opencode",
			factory.RoleReview: "agy",
		},
	}
	if got := cfgFallback.AgentForRole(factory.RoleProbe); got != "opencode" {
		t.Errorf("expected probe to fall back to coding 'opencode', got %q", got)
	}
	if got := cfgFallback.AgentForRole(factory.RoleSpec); got != "agy" {
		t.Errorf("expected spec 'agy', got %q", got)
	}
	if got := cfgFallback.AgentForRole(factory.RoleCoding); got != "opencode" {
		t.Errorf("expected coding 'opencode', got %q", got)
	}
	if got := cfgFallback.AgentForRole(factory.RoleReview); got != "agy" {
		t.Errorf("expected review 'agy', got %q", got)
	}

	// Case 3: Empty/missing role
	cfgEmpty := factory.ProjectConfig{}
	if got := cfgEmpty.AgentForRole(factory.RoleCoding); got != "" {
		t.Errorf("expected empty string for unconfigured role, got %q", got)
	}
	if got := cfgEmpty.AgentForRole(factory.RoleProbe); got != "" {
		t.Errorf("expected empty string for unconfigured probe, got %q", got)
	}
}
