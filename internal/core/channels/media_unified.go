package channels

import (
	"net/http"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/channels/shared"
)

const (
	UnifiedMediaImage = shared.UnifiedMediaImage
	UnifiedMediaFile  = shared.UnifiedMediaFile
	UnifiedMediaVideo = shared.UnifiedMediaVideo
	UnifiedMediaAudio = shared.UnifiedMediaAudio
)

func NormalizeMediaType(t string) string {
	return shared.NormalizeMediaType(t)
}

func SelectFirstSupportedMedia(list []bus.Media, supported map[string]bool) (bus.Media, bool) {
	return shared.SelectFirstSupportedMedia(list, supported)
}

func DecodeBase64Media(raw string) ([]byte, error) {
	return shared.DecodeBase64Media(raw)
}

func InferMediaFileName(m bus.Media, fallback string) string {
	return shared.InferMediaFileName(m, fallback)
}

func MaterializeMediaData(client *http.Client, media bus.Media, maxBytes int64) ([]byte, error) {
	return shared.MaterializeMediaData(client, media, maxBytes)
}

func AppendMediaURLsToContent(content string, list []bus.Media, supported map[string]bool) string {
	return shared.AppendMediaURLsToContent(content, list, supported)
}
