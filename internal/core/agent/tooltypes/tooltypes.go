package tooltypes

import "context"

type ConcurrencyMode string

const (
	ConcurrencyExclusive  ConcurrencyMode = "exclusive"
	ConcurrencyConcurrent ConcurrencyMode = "concurrent"
)

type MutationKind string

const (
	MutationRead          MutationKind = "read"
	MutationWrite         MutationKind = "write"
	MutationSideEffect    MutationKind = "side_effect"
	MutationExternal      MutationKind = "external"
	MutationOrchestration MutationKind = "orchestration"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// ToolSpec carries execution metadata that the runtime can consume without
// changing the public tool interface. It is intentionally optional so tools can
// adopt it incrementally.
type ToolSpec struct {
	Name             string
	Concurrency      ConcurrencyMode
	Mutation         MutationKind
	Risk             RiskLevel
	DefaultTimeout   int
	PrefersSandbox   bool
	RequiresApproval bool
	Tags             []string
}

// Normalized returns a copy with sane defaults filled in.
func (s ToolSpec) Normalized(name string) ToolSpec {
	if s.Name == "" {
		s.Name = name
	}
	if s.Concurrency == "" {
		s.Concurrency = ConcurrencyExclusive
	}
	if s.Mutation == "" {
		s.Mutation = MutationSideEffect
	}
	if s.Risk == "" {
		s.Risk = RiskMedium
	}
	if s.Tags == nil {
		s.Tags = []string{}
	}
	return s
}

// ToolSpecProvider is implemented by tools that expose structured runtime
// metadata in addition to the execution interface.
type ToolSpecProvider interface {
	Spec() ToolSpec
}

// AgentToolInterface defines the interface for agent tools.
// This avoids circular dependency between agent/tools and agent packages.
type AgentToolInterface interface {
	Name() string
	Description() string
	Label() string
	Parameters() map[string]any
	Execute(ctx context.Context, params map[string]any, onUpdate func(AgentToolResult)) (AgentToolResult, error)
}

// AgentToolResult represents the result of an agent tool execution.
type AgentToolResult struct {
	Content []ContentBlock
	Details map[string]any
	Error   error
}

// ContentBlock represents a content block in a message.
type ContentBlock interface {
	ContentType() string
}

// TextContent represents text content.
type TextContent struct {
	Text string
}

func (t TextContent) ContentType() string {
	return "text"
}

// AgentTextContent is a text content block (alias for tools package compatibility).
type AgentTextContent = TextContent

// AgentContentBlock represents a content block from agent tools (alias).
type AgentContentBlock = ContentBlock

// ToolResult is an alias for AgentToolResult (for tools package compatibility).
type ToolResult = AgentToolResult
