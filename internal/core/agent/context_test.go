package agent

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptIncludesHumanCommunicationGuidance(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	prompt := builder.BuildSystemPrompt(nil)

	checks := []string{
		"## Communication Style",
		"收到，我先帮你看下这个问题。",
		"我在帮你处理，您稍等一下。",
		`Do not fake a process with "我先看下" when no real waiting or tool work is needed.`,
		"Do not fragment a normal answer into many small messages just because you can.",
		"emotional support, comforting, or soft check-in moments where two short beats feel more human than one polished paragraph",
		"when the user is sharing feelings or feeling low, default to two short messages instead of one overly complete block",
		"哎，心情不好的时候真的很难受。",
		"发生什么事了？想说就说，我听着。",
		"Prefer 'send_message' when you want deliberate acknowledgement, progress reporting, or exact control over whether the user sees one message or several",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing guidance %q", want)
		}
	}
}

func TestBuildSystemPromptListsProgressMessagingTools(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	prompt := builder.BuildSystemPrompt(nil)

	checks := []string{
		"- send_message:",
		"- send_file:",
		"- sessions_spawn:",
		"- memory_search:",
		"- memory_add:",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing tool summary %q", want)
		}
	}
}
