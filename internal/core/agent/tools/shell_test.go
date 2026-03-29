package tools

import (
	"strings"
	"testing"
)

func TestFormatShellCommandOutputReturnsRawStdoutForSmallOutput(t *testing.T) {
	stdout := newShellOutputCollector()
	if _, err := stdout.Write([]byte("hello\nworld\n")); err != nil {
		t.Fatalf("stdout.Write error: %v", err)
	}

	got := formatShellCommandOutput(stdout, newShellOutputCollector())
	if got != "hello\nworld\n" {
		t.Fatalf("formatShellCommandOutput = %q, want raw stdout", got)
	}
}

func TestFormatShellCommandOutputUsesPreviewForLargeOutput(t *testing.T) {
	stdout := newShellOutputCollector()
	payload := strings.Repeat("A", shellFullOutputBytes) + "MIDDLE" + strings.Repeat("B", shellFullOutputBytes)
	if _, err := stdout.Write([]byte(payload)); err != nil {
		t.Fatalf("stdout.Write error: %v", err)
	}

	got := formatShellCommandOutput(stdout, newShellOutputCollector())
	if !strings.Contains(got, "[stdout]") {
		t.Fatalf("expected stdout section label, got %q", got)
	}
	if !strings.Contains(got, "omitted") {
		t.Fatalf("expected omitted note, got %q", got)
	}
	if !strings.Contains(got, strings.Repeat("A", 32)) {
		t.Fatalf("expected head content in preview, got %q", got)
	}
	if !strings.Contains(got, strings.Repeat("B", 32)) {
		t.Fatalf("expected tail content in preview, got %q", got)
	}
	if strings.Contains(got, "MIDDLE") {
		t.Fatalf("did not expect middle content to be preserved in preview")
	}
}

func TestFormatShellCommandOutputSeparatesStdoutAndStderr(t *testing.T) {
	stdout := newShellOutputCollector()
	stderr := newShellOutputCollector()
	if _, err := stdout.Write([]byte("ok\n")); err != nil {
		t.Fatalf("stdout.Write error: %v", err)
	}
	if _, err := stderr.Write([]byte("warn\n")); err != nil {
		t.Fatalf("stderr.Write error: %v", err)
	}

	got := formatShellCommandOutput(stdout, stderr)
	if !strings.Contains(got, "[stdout]") || !strings.Contains(got, "[stderr]") {
		t.Fatalf("expected separate stream sections, got %q", got)
	}
}

func TestFormatShellCommandOutputSummarizesLongLineOutput(t *testing.T) {
	stdout := newShellOutputCollector()
	longLine := strings.Repeat("x", shellSummaryMaxLineRunes+80) + "\n"
	if _, err := stdout.Write([]byte(longLine)); err != nil {
		t.Fatalf("stdout.Write error: %v", err)
	}

	got := formatShellCommandOutput(stdout, newShellOutputCollector())
	if !strings.Contains(got, "...(truncated)") {
		t.Fatalf("expected long line truncation, got %q", got)
	}
	if !strings.Contains(got, "long line(s) truncated") {
		t.Fatalf("expected truncation note, got %q", got)
	}
}

func TestFormatShellExecErrorTruncatesOutputPreview(t *testing.T) {
	err := formatShellExecError(assertionError("boom"), strings.Repeat("z", shellErrorPreviewRunes+100))
	if !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("expected wrapped error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "error output truncated") {
		t.Fatalf("expected truncation note, got %q", err.Error())
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }
