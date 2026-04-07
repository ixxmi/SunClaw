import { computed, ref } from "vue";
import { sendChatMessage, type ChatMessage, type ChatTransportMode } from "../api/chat";

function createLocalMessage(role: ChatMessage["role"], content: string, mode?: ChatTransportMode): ChatMessage {
  return {
    id: `${role}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    role,
    content,
    createdAt: Date.now(),
    mode,
  };
}

export function useChatPanel() {
  const sessionId = ref("default-chat-session");
  const messages = ref<ChatMessage[]>([
    createLocalMessage(
      "assistant",
      "你好，这里是 SunClaw Chat。当前实现为最小前端会话面板：优先请求 live chat API，不可用时自动回退到 mock 回复。",
      "mock",
    ),
  ]);
  const input = ref("");
  const sending = ref(false);
  const error = ref("");
  const mode = ref<ChatTransportMode>("mock");
  const fallbackReason = ref("");

  const canSend = computed(() => input.value.trim().length > 0 && !sending.value);

  function applyQuickAction(value: string) {
    input.value = value.trim();
    error.value = "";
  }

  function clearMessages() {
    messages.value = [
      createLocalMessage(
        "assistant",
        "会话已重置。当前仍会优先尝试 live chat API，失败时回退 mock。",
        mode.value,
      ),
    ];
    error.value = "";
    fallbackReason.value = "";
  }

  async function sendMessage(override?: string) {
    const content = (override ?? input.value).trim();
    if (!content || sending.value) {
      return;
    }

    sending.value = true;
    error.value = "";
    fallbackReason.value = "";

    const userMessage = createLocalMessage("user", content);
    messages.value = [...messages.value, userMessage];
    input.value = "";

    try {
      const result = await sendChatMessage({
        sessionId: sessionId.value,
        message: content,
        history: messages.value,
      });

      mode.value = result.mode;
      fallbackReason.value = result.fallbackReason ?? "";
      messages.value = [...messages.value, result.message];
    } catch (err) {
      const message = err instanceof Error ? err.message : "failed to send chat message";
      error.value = message;
      input.value = content;
      messages.value = messages.value.filter((item) => item.id !== userMessage.id);
    } finally {
      sending.value = false;
    }
  }

  return {
    sessionId,
    messages,
    input,
    sending,
    error,
    mode,
    fallbackReason,
    canSend,
    applyQuickAction,
    clearMessages,
    sendMessage,
  };
}
