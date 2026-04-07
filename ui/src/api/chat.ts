export type ChatRole = "user" | "assistant" | "system";
export type ChatTransportMode = "live" | "mock";

export interface ChatMessage {
  id: string;
  role: ChatRole;
  content: string;
  createdAt: number;
  mode?: ChatTransportMode;
}

export interface ChatSendPayload {
  sessionId: string;
  message: string;
  history: ChatMessage[];
}

export interface ChatSendResult {
  message: ChatMessage;
  mode: ChatTransportMode;
  fallbackReason?: string;
}

interface LiveChatResponse {
  reply?: string;
  message?: string;
  content?: string;
  output?: string;
  answer?: string;
}

function createMessage(content: string, mode: ChatTransportMode): ChatMessage {
  return {
    id: `chat-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    role: "assistant",
    content,
    createdAt: Date.now(),
    mode,
  };
}

function extractReply(payload: unknown): string {
  if (!payload || typeof payload !== "object") {
    return "";
  }

  const candidate = payload as LiveChatResponse;
  const text = candidate.reply ?? candidate.message ?? candidate.content ?? candidate.output ?? candidate.answer ?? "";
  return typeof text === "string" ? text.trim() : "";
}

function buildMockReply(message: string): string {
  const normalized = message.trim();
  if (!normalized) {
    return "我已收到空消息，但当前只启用了 mock 回复，请输入具体问题或操作。";
  }

  if (/health|检查|巡检/i.test(normalized)) {
    return "Mock 模式：网关健康检查入口已预留。当前前端已连通发送流程，但后端 chat API 未确认，因此返回占位结果。";
  }

  if (/refresh|刷新|reload/i.test(normalized)) {
    return "Mock 模式：页面刷新仍由顶部 Refresh 按钮负责；这里仅模拟对话回复。";
  }

  return `Mock 模式回复：已收到你的消息“${normalized}”。当前会优先尝试 /api/chat；若接口不可用或返回格式未知，则自动回退到该占位回复。`;
}

export async function sendChatMessage(payload: ChatSendPayload, signal?: AbortSignal): Promise<ChatSendResult> {
  try {
    const response = await fetch("/api/chat", {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        sessionId: payload.sessionId,
        message: payload.message,
        history: payload.history,
      }),
      signal,
    });

    if (!response.ok) {
      throw new Error(`chat request failed: ${response.status}`);
    }

    const data = (await response.json()) as unknown;
    const reply = extractReply(data);
    if (!reply) {
      throw new Error("chat response missing reply text");
    }

    return {
      message: createMessage(reply, "live"),
      mode: "live",
    };
  } catch (error) {
    const reason = error instanceof Error ? error.message : "chat live request unavailable";
    return {
      message: createMessage(buildMockReply(payload.message), "mock"),
      mode: "mock",
      fallbackReason: reason,
    };
  }
}
