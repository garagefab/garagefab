// Package factory contains the core domain model, pipeline engine, and scheduler
// for the software factory.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Pure Domain Logic / Role Mapping & Resolution (Hexagonal / Clean Architecture).
//
// In Clean Architecture and DDD:
//  1. Pure Domain Logic:
//     `roles.go` provides deterministic, side-effect-free mappings between SDLC
//     pipeline stages (e.g. StageCoding) and agent roles (e.g. RoleCoding) as
//     required by HND-2. It performs no I/O, runs no processes, and makes no network calls.
//  2. Enterprise / Java Comparison:
//     In Java/Spring enterprise backends, this is equivalent to a Domain Service
//     or Policy enum:
//     `com.garagefab.domain.service.AgentRolePolicy`
//     It centralizes stage-to-role mappings so controllers, schedulers, and adapters
//     never hardcode role strings.
//
// GO CONCEPTS & IDIOMS:
//  1. Multi-Value Return with `ok` boolean:
//     `RoleForStage` returns `(role string, ok bool)`. This is the standard Go idiom
//     (comma-ok pattern) for operations that may not find a valid mapping, avoiding
//     sentinel values or exceptions.
//  2. Value Receiver on Struct (`func (c ProjectConfig) AgentForRole(role string) string`):
//     Go allows methods on value types as well as pointers. Since `ProjectConfig`
//     is not modified by this method, a value receiver is safe and idiomatic.
//
// ==============================================================================
package factory

// SDLC Agent Roles (HND-2, architecture.md §14).
const (
	RoleSpec   = "spec"   // Clarification interview and spec generation (02_Clarification_and_Spec)
	RoleProbe  = "probe"  // Reproducing probe / feature absence test (03_Failing_Probe)
	RoleCoding = "coding" // Production code and unit test implementation (04_Coding, 06_Human_Approval_Gate)
	RoleReview = "review" // Independent diff critique and risk assessment (05_Independent_Review)
)

// RoleForStage maps an SDLC pipeline stage name or numeric prefix to its designated
// AI agent role (HND-2). Returns ok=false for stages that do not invoke an agent.
func RoleForStage(stage string) (role string, ok bool) {
	switch stage {
	case StageClarificationAndSpec, "02":
		return RoleSpec, true
	case StageFailingProbe, "03":
		return RoleProbe, true
	case StageCoding, "04":
		return RoleCoding, true
	case StageIndependentReview, "05":
		return RoleReview, true
	case StageHumanApprovalGate, "06":
		return RoleCoding, true
	default:
		return "", false
	}
}

// AgentForRole resolves the configured agent name (e.g. "agy" or "opencode") for a given role.
// In accordance with HND-2:
// - If agents.probe is unset, probe automatically falls back to the coding agent.
// - Returns an empty string if no agent is configured for the role.
func (c ProjectConfig) AgentForRole(role string) string {
	if c.Agents == nil {
		return ""
	}
	if role == RoleProbe {
		if a, ok := c.Agents[RoleProbe]; ok && a != "" {
			return a
		}
		return c.Agents[RoleCoding]
	}
	return c.Agents[role]
}
