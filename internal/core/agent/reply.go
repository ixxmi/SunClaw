package agent

import "strings"

const (
	silentReplyToken  = "SILENT_REPLY"
	heartbeatAckToken = "HEARTBEAT_OK"
)

// findLatestReplyableAssistantMessage returns the newest assistant message that
// should be sent back to the user. It skips empty assistant messages and stops
// on explicit silent/heartbeat sentinels to avoid duplicate sends.
func findLatestReplyableAssistantMessage(messages []AgentMessage) *AgentMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != RoleAssistant {
			continue
		}

		text := strings.TrimSpace(extractTextContent(msg))
		switch text {
		case "":
			continue
		case silentReplyToken, heartbeatAckToken:
			return nil
		default:
			reply := msg
			return &reply
		}
	}

	return nil
}
