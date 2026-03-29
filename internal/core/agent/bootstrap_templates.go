package agent

import (
	"strings"

	"github.com/smallnest/goclaw/internal/workspace"
)

type bootstrapTemplates struct {
	Soul           string
	Identity       string
	Agents         string
	User           string
	BootstrapGuide string
}

func (b *ContextBuilder) resolveBootstrapTemplates() bootstrapTemplates {
	readTemplate := func(filename string) string {
		content, err := workspace.ReadEmbeddedTemplate(filename)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(content)
	}

	return bootstrapTemplates{
		Soul:           readTemplate("SOUL.md"),
		Identity:       readTemplate("IDENTITY.md"),
		Agents:         readTemplate("AGENTS.md"),
		User:           readTemplate("USER.md"),
		BootstrapGuide: readTemplate("BOOTSTRAP.md"),
	}
}
