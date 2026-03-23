package agent

import "testing"

func TestConvertToProviderMessagesPrefixesBase64ImageData(t *testing.T) {
	msgs := []AgentMessage{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				TextContent{Text: "[图片]\n这是什么"},
				ImageContent{Data: "YWJj", MimeType: "image/png"},
			},
		},
	}

	got := convertToProviderMessages(msgs)
	if len(got) != 1 {
		t.Fatalf("expected 1 provider message, got %d", len(got))
	}
	if len(got[0].Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(got[0].Images))
	}
	if got[0].Images[0] != "data:image/png;base64,YWJj" {
		t.Fatalf("unexpected image payload: %q", got[0].Images[0])
	}
}

func TestFormatProviderImageDataURLKeepsExistingURL(t *testing.T) {
	got := formatProviderImageDataURL("image/png", "https://example.com/a.png")
	if got != "https://example.com/a.png" {
		t.Fatalf("unexpected formatted url: %q", got)
	}
}
