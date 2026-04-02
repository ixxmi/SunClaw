package permissions

import "github.com/smallnest/goclaw/internal/core/agent/tooltypes"

type Disposition string

const (
	DispositionAllow Disposition = "allow"
	DispositionDeny  Disposition = "deny"
)

type Decision struct {
	Disposition      Disposition
	Reason           string
	MatchedRule      string
	RequiresApproval bool
	Spec             tooltypes.ToolSpec
}

func (d Decision) Allowed() bool {
	return d.Disposition != DispositionDeny
}

type Policy struct {
	Mode         string
	allowlist    map[string]struct{}
	shellAllowed []string
	shellDenied  []string
}
