package channels

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/smallnest/goclaw/internal/core/bus"
)

func TestNormalizeMediaType(t *testing.T) {
	cases := map[string]string{
		"image":      UnifiedMediaImage,
		"PHOTO":      UnifiedMediaImage,
		"document":   UnifiedMediaFile,
		"attachment": UnifiedMediaFile,
		"voice":      UnifiedMediaAudio,
		"video":      UnifiedMediaVideo,
	}

	for in, want := range cases {
		if got := NormalizeMediaType(in); got != want {
			t.Fatalf("NormalizeMediaType(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDecodeBase64Media_DataURI(t *testing.T) {
	raw := []byte("hello-media")
	payload := "data:text/plain;base64," + base64.StdEncoding.EncodeToString(raw)

	got, err := DecodeBase64Media(payload)
	if err != nil {
		t.Fatalf("DecodeBase64Media returned err: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("decoded data mismatch got=%q want=%q", string(got), string(raw))
	}
}

func TestAppendMediaURLsToContent(t *testing.T) {
	content := "base"
	media := []bus.Media{
		{Type: "image", URL: "https://a/img.png"},
		{Type: "file", URL: "https://a/file.pdf"},
		{Type: "audio", URL: "https://a/audio.mp3"},
		{Type: "file", URL: ""},
	}

	got := AppendMediaURLsToContent(content, media, map[string]bool{
		UnifiedMediaFile:  true,
		UnifiedMediaAudio: true,
	})

	if !strings.Contains(got, "[file] https://a/file.pdf") {
		t.Fatalf("missing file line: %s", got)
	}
	if !strings.Contains(got, "[audio] https://a/audio.mp3") {
		t.Fatalf("missing audio line: %s", got)
	}
	if strings.Contains(got, "img.png") {
		t.Fatalf("unexpected image line: %s", got)
	}
}
