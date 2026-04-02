package agent

import (
	"strings"

	"github.com/smallnest/goclaw/internal/core/agent/tooltypes"
)

// ResolveToolSpec returns structured runtime metadata for a tool.
//
// Tools can expose an explicit Spec() method, but we also keep conservative
// name-based defaults so older tools immediately participate in the metadata
// model without behavioral changes.
func ResolveToolSpec(tool Tool) tooltypes.ToolSpec {
	if tool == nil {
		return tooltypes.ToolSpec{}.Normalized("")
	}

	if provider, ok := tool.(tooltypes.ToolSpecProvider); ok {
		return provider.Spec().Normalized(tool.Name())
	}

	return defaultToolSpec(tool.Name())
}

func defaultToolSpec(name string) tooltypes.ToolSpec {
	spec := tooltypes.ToolSpec{Name: name}

	switch strings.TrimSpace(name) {
	case "read_file", "list_files", "glob_files", "grep_content", "web_search", "web_fetch", "use_skill", "session_status", "memory_search":
		spec.Concurrency = tooltypes.ConcurrencyConcurrent
		spec.Mutation = tooltypes.MutationRead
		spec.Risk = tooltypes.RiskLow
	case "write_file", "edit_file":
		spec.Concurrency = tooltypes.ConcurrencyExclusive
		spec.Mutation = tooltypes.MutationWrite
		spec.Risk = tooltypes.RiskMedium
	case "update_config":
		spec.Concurrency = tooltypes.ConcurrencyExclusive
		spec.Mutation = tooltypes.MutationWrite
		spec.Risk = tooltypes.RiskHigh
		spec.RequiresApproval = true
	case "run_shell", "sandbox_execute":
		spec.Concurrency = tooltypes.ConcurrencyExclusive
		spec.Mutation = tooltypes.MutationSideEffect
		spec.Risk = tooltypes.RiskHigh
		spec.RequiresApproval = true
		if name == "sandbox_execute" {
			spec.PrefersSandbox = true
		}
	case "sessions_spawn", "spawn_acp":
		spec.Concurrency = tooltypes.ConcurrencyExclusive
		spec.Mutation = tooltypes.MutationOrchestration
		spec.Risk = tooltypes.RiskHigh
		spec.RequiresApproval = true
	case "send_message", "send_file", "message", "reminder", "cron", "memory_add":
		spec.Concurrency = tooltypes.ConcurrencyExclusive
		spec.Mutation = tooltypes.MutationSideEffect
		spec.Risk = tooltypes.RiskMedium
	default:
		spec.Concurrency = tooltypes.ConcurrencyExclusive
		spec.Mutation = tooltypes.MutationSideEffect
		spec.Risk = tooltypes.RiskMedium
	}

	return spec.Normalized(name)
}
