import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch, type Ref } from "vue";
import type { ShrimpBrainRun, ShrimpBrainSnapshot, ShrimpSessionGroup } from "../data/dashboard";
import { deleteShrimpBrainRun, fetchShrimpBrainSnapshot } from "../api/shrimpBrain";

const SHRIMP_REPLY_COLLAPSE_LINES = 8;
const SHRIMP_REPLY_COLLAPSE_THRESHOLD = 240;

export function groupShrimpRunsBySession(runs: ShrimpBrainRun[]): ShrimpSessionGroup[] {
  const groups = new Map<string, ShrimpSessionGroup>();

  for (const run of runs) {
    const key = run.blockKey || run.sessionKey || run.id;
    const existing = groups.get(key);
    if (!existing) {
      groups.set(key, {
        blockKey: key,
        userKey: run.userKey,
        sessionKey: run.sessionKey,
        channel: run.channel,
        chatId: run.chatId,
        mainAgentId: run.mainAgentId,
        status: run.status,
        startedAt: run.startedAt,
        updatedAt: run.updatedAt,
        runs: [run],
      });
      continue;
    }

    existing.runs.push(run);
    existing.startedAt = Math.min(existing.startedAt, run.startedAt);
    existing.updatedAt = Math.max(existing.updatedAt, run.updatedAt);
    existing.status = run.status === "completed" && existing.status === "completed" ? "completed" : run.status;
  }

  const sessions = Array.from(groups.values());
  for (const session of sessions) {
    session.runs.sort((a, b) => a.startedAt - b.startedAt);
  }
  sessions.sort((a, b) => b.updatedAt - a.updatedAt);
  return sessions;
}

export function shrimpStatusBucket(status: string) {
  switch (status.trim().toLowerCase()) {
    case "completed":
    case "ok":
      return "completed";
    case "live":
    case "connecting":
    case "reconnecting":
    case "running":
    case "assigned":
    case "planning":
    case "ready":
      return "running";
    case "failed":
    case "error":
    case "timeout":
      return "failed";
    default:
      return "pending";
  }
}

export function shrimpStatusBadgeClass(status: string) {
  return `shrimp-status-badge is-${shrimpStatusBucket(status)}`;
}

export function isFailureStatus(status: string) {
  return shrimpStatusBucket(status) === "failed";
}

export function formatShrimpTimeShort(timestamp?: number) {
  if (!timestamp) return "";
  const d = new Date(timestamp);
  return `${d.getHours().toString().padStart(2, "0")}:${d.getMinutes().toString().padStart(2, "0")}:${d.getSeconds().toString().padStart(2, "0")}`;
}

export function formatShrimpTime(timestamp?: number) {
  if (!timestamp) {
    return "";
  }
  return new Date(timestamp).toLocaleString("zh-CN");
}

export function truncateShrimpText(value: string | undefined, limit = 20) {
  const compact = value?.trim() ?? "";
  if (!compact) {
    return "-";
  }
  const chars = Array.from(compact);
  if (chars.length <= limit) {
    return compact;
  }
  return `${chars.slice(0, limit).join("")}...`;
}

export function shouldShowShrimpReplyToggle(reply?: string) {
  const content = reply?.trim() ?? "";
  if (!content) {
    return false;
  }
  const lineCount = content.split(/\r?\n/).length;
  return lineCount > SHRIMP_REPLY_COLLAPSE_LINES || content.length > SHRIMP_REPLY_COLLAPSE_THRESHOLD;
}

export function shrimpTurnTitle(index: number) {
  return `Turn ${index + 1}`;
}

export function useShrimpBrainPanel() {
  const shrimpBrain = ref<ShrimpBrainSnapshot | null>(null);
  const shrimpBrainLoading = ref(false);
  const shrimpBrainError = ref("");
  const shrimpBrainStreamState = ref("connecting");
  const selectedShrimpSessionKey = ref("");
  const deletingShrimpRunId = ref("");
  const expandedShrimpReplies = ref<Record<string, boolean>>({});
  const expandedShrimpTurns = ref<Record<string, boolean>>({});
  const shrimpEventStreamRef = ref<HTMLElement | null>(null);
  const isShrimpAutoFollowEnabled = ref(true);
  const showShrimpFollowButton = ref(false);
  const shrimpPendingUpdateCount = ref(0);
  const shrimpFollowNotice = ref("");
  let shrimpBrainStream: EventSource | null = null;
  let shrimpFollowNoticeTimer: number | undefined;
  let lastShrimpRunsSignature = "";

  const shrimpRuns = computed(() => shrimpBrain.value?.runs ?? []);
  const shrimpSessions = computed<ShrimpSessionGroup[]>(() => groupShrimpRunsBySession(shrimpRuns.value));
  const shrimpBrainGeneratedAtLabel = computed(() => {
    if (!shrimpBrain.value?.generatedAt) {
      return "";
    }
    return new Date(shrimpBrain.value.generatedAt).toLocaleString("zh-CN");
  });
  const selectedShrimpSession = computed(() => {
    const sessions = shrimpSessions.value;
    if (!sessions.length) {
      return null;
    }
    return sessions.find((session) => session.blockKey === selectedShrimpSessionKey.value) ?? sessions[0];
  });
  const selectedShrimpSessionLabel = computed(() => {
    const session = selectedShrimpSession.value;
    if (!session) {
      return "";
    }
    return `${session.mainAgentId || "main"} · ${session.channel || "cli"} · ${formatShrimpTimeShort(session.updatedAt)}`;
  });
  const shrimpStreamStateText = computed(() => {
    switch (shrimpBrainStreamState.value) {
      case "live":
        return "SSE 实时同步中";
      case "connecting":
        return "SSE 连接中";
      case "reconnecting":
        return "SSE 重连中，暂用快照兜底";
      case "unsupported":
        return "当前环境不支持 SSE，使用手动/定时刷新";
      default:
        return shrimpBrainStreamState.value || "SSE 空闲";
    }
  });
  const shrimpAutoFollowButtonText = computed(() => {
    if (shrimpPendingUpdateCount.value > 0 && !isShrimpAutoFollowEnabled.value) {
      return `查看 ${shrimpPendingUpdateCount.value} 条新内容`;
    }
    return isShrimpAutoFollowEnabled.value ? "已跟随到底部" : "恢复自动跟随";
  });

  function syncSelectedShrimpRun() {
    const sessions = shrimpSessions.value;
    if (!sessions.length) {
      selectedShrimpSessionKey.value = "";
      return;
    }
    if (!selectedShrimpSessionKey.value || !sessions.some((session) => session.blockKey === selectedShrimpSessionKey.value)) {
      selectedShrimpSessionKey.value = sessions[0].blockKey;
    }
  }

  async function loadShrimpBrain(options: { silent?: boolean } = {}) {
    const silent = options.silent === true;
    if (!silent || !shrimpBrain.value) {
      shrimpBrainLoading.value = true;
    }
    if (!silent) {
      shrimpBrainError.value = "";
    }

    try {
      shrimpBrain.value = await fetchShrimpBrainSnapshot();
      syncSelectedShrimpRun();
    } catch (err) {
      const message = err instanceof Error ? err.message : "failed to load shrimp brain";
      shrimpBrainError.value = message;
    } finally {
      if (!silent || !shrimpBrain.value) {
        shrimpBrainLoading.value = false;
      }
    }
  }

  async function handleDeleteShrimpRun(runId: string) {
    if (!runId || deletingShrimpRunId.value) {
      return;
    }

    const confirmed = window.confirm("删除后该条虾脑协作记录会从本地持久化中移除，是否继续？");
    if (!confirmed) {
      return;
    }

    deletingShrimpRunId.value = runId;
    try {
      await deleteShrimpBrainRun(runId);
      await loadShrimpBrain({ silent: true });
    } catch (err) {
      shrimpBrainError.value = err instanceof Error ? err.message : "failed to delete shrimp brain run";
    } finally {
      deletingShrimpRunId.value = "";
    }
  }

  function connectShrimpBrainStream() {
    if (shrimpBrainStream) {
      shrimpBrainStream.close();
      shrimpBrainStream = null;
    }

    if (typeof window === "undefined" || typeof window.EventSource === "undefined") {
      shrimpBrainStreamState.value = "unsupported";
      return;
    }

    shrimpBrainStreamState.value = "connecting";
    shrimpBrainStream = new EventSource("/api/shrimp-brain/stream");

    shrimpBrainStream.onopen = () => {
      shrimpBrainStreamState.value = "live";
    };

    shrimpBrainStream.onmessage = (event) => {
      try {
        shrimpBrain.value = JSON.parse(event.data) as ShrimpBrainSnapshot;
        shrimpBrainError.value = "";
        syncSelectedShrimpRun();
      } catch (err) {
        shrimpBrainError.value = err instanceof Error ? err.message : "failed to parse shrimp brain event";
      }
    };

    shrimpBrainStream.onerror = () => {
      shrimpBrainStreamState.value = "reconnecting";
    };
  }

  function closeShrimpBrainStream() {
    if (shrimpBrainStream) {
      shrimpBrainStream.close();
      shrimpBrainStream = null;
    }
  }

  function setShrimpFromSnapshot(nextSnapshot: ShrimpBrainSnapshot | null) {
    if (!nextSnapshot) {
      return;
    }
    if (!shrimpBrain.value) {
      shrimpBrain.value = nextSnapshot;
      syncSelectedShrimpRun();
    }
  }

  function selectShrimpSession(blockKey: string) {
    if (!blockKey || selectedShrimpSessionKey.value === blockKey) {
      return;
    }
    selectedShrimpSessionKey.value = blockKey;
  }

  function isNearShrimpEventStreamBottom() {
    const container = shrimpEventStreamRef.value;
    if (!container) {
      return true;
    }
    return container.scrollHeight - container.scrollTop - container.clientHeight <= 48;
  }

  function setShrimpFollowNotice(message: string, duration = 2400) {
    shrimpFollowNotice.value = message;
    if (shrimpFollowNoticeTimer) {
      window.clearTimeout(shrimpFollowNoticeTimer);
    }
    if (!message) {
      return;
    }
    shrimpFollowNoticeTimer = window.setTimeout(() => {
      shrimpFollowNotice.value = "";
    }, duration);
  }

  function shrimpFollowStatusLabel() {
    if (shrimpPendingUpdateCount.value > 0 && !isShrimpAutoFollowEnabled.value) {
      return `已暂停自动跟随 · 有 ${shrimpPendingUpdateCount.value} 条新内容`;
    }
    return isShrimpAutoFollowEnabled.value ? "自动跟随中" : "已暂停自动跟随";
  }

  function scrollShrimpEventStreamToBottom(force = false) {
    const container = shrimpEventStreamRef.value;
    if (!container) {
      return;
    }
    if (!force && !isShrimpAutoFollowEnabled.value) {
      return;
    }
    container.scrollTop = container.scrollHeight;
  }

  function handleShrimpEventStreamScroll() {
    const isAtBottom = isNearShrimpEventStreamBottom();
    showShrimpFollowButton.value = !isAtBottom || shrimpPendingUpdateCount.value > 0;
    if (isAtBottom) {
      if (!isShrimpAutoFollowEnabled.value) {
        setShrimpFollowNotice("已回到底部，恢复自动跟随");
      }
      isShrimpAutoFollowEnabled.value = true;
      shrimpPendingUpdateCount.value = 0;
      return;
    }
    if (shrimpEventStreamRef.value && isShrimpAutoFollowEnabled.value) {
      setShrimpFollowNotice("你已离开底部，自动跟随已暂停");
    }
    if (shrimpEventStreamRef.value) {
      isShrimpAutoFollowEnabled.value = false;
    }
  }

  function restoreShrimpAutoFollow() {
    isShrimpAutoFollowEnabled.value = true;
    shrimpPendingUpdateCount.value = 0;
    showShrimpFollowButton.value = false;
    setShrimpFollowNotice("已恢复自动跟随");
    void nextTick(() => {
      scrollShrimpEventStreamToBottom(true);
    });
  }

  function isShrimpReplyExpanded(runId: string) {
    return Boolean(expandedShrimpReplies.value[runId]);
  }

  function toggleShrimpReply(runId: string) {
    expandedShrimpReplies.value = {
      ...expandedShrimpReplies.value,
      [runId]: !expandedShrimpReplies.value[runId],
    };
  }

  function isShrimpTurnExpanded(runId: string) {
    const explicit = expandedShrimpTurns.value[runId];
    if (typeof explicit === "boolean") {
      return explicit;
    }
    const runs = selectedShrimpSession.value?.runs ?? [];
    return runs.length > 0 && runs[runs.length - 1]?.id === runId;
  }

  function toggleShrimpTurn(runId: string) {
    expandedShrimpTurns.value = {
      ...expandedShrimpTurns.value,
      [runId]: !expandedShrimpTurns.value[runId],
    };
  }

  watch(selectedShrimpSessionKey, async () => {
    isShrimpAutoFollowEnabled.value = true;
    showShrimpFollowButton.value = false;
    shrimpPendingUpdateCount.value = 0;
    shrimpFollowNotice.value = "";
    await nextTick();
    scrollShrimpEventStreamToBottom(true);
  });

  watch(
    () => selectedShrimpSession.value?.runs.map((run) => `${run.id}:${run.updatedAt ?? run.startedAt ?? 0}`).join("|") ?? "",
    async (signature) => {
      const runs = selectedShrimpSession.value?.runs ?? [];
      const latestRunId = runs[runs.length - 1]?.id;
      if (latestRunId) {
        expandedShrimpTurns.value = Object.fromEntries(runs.slice(0, -1).map((run) => [run.id, false]));
        expandedShrimpTurns.value[latestRunId] = true;
      } else {
        expandedShrimpTurns.value = {};
      }

      const previousSignature = lastShrimpRunsSignature;
      const hadPreviousSnapshot = previousSignature !== "";
      const signatureChanged = previousSignature !== signature;
      lastShrimpRunsSignature = signature;

      await nextTick();

      if (signatureChanged && hadPreviousSnapshot && !isShrimpAutoFollowEnabled.value) {
        shrimpPendingUpdateCount.value += 1;
        showShrimpFollowButton.value = true;
        setShrimpFollowNotice(`有 ${shrimpPendingUpdateCount.value} 条新内容，点击可恢复跟随`, 3200);
        return;
      }

      if (isShrimpAutoFollowEnabled.value) {
        shrimpPendingUpdateCount.value = 0;
        scrollShrimpEventStreamToBottom();
        showShrimpFollowButton.value = !isNearShrimpEventStreamBottom();
        return;
      }

      showShrimpFollowButton.value = !isNearShrimpEventStreamBottom() || shrimpPendingUpdateCount.value > 0;
    },
    { immediate: true },
  );

  return {
    shrimpBrain,
    shrimpBrainLoading,
    shrimpBrainError,
    shrimpBrainStreamState,
    shrimpRuns,
    shrimpSessions,
    shrimpBrainGeneratedAtLabel,
    selectedShrimpSessionKey,
    selectedShrimpSession,
    selectedShrimpSessionLabel,
    deletingShrimpRunId,
    shrimpEventStreamRef,
    isShrimpAutoFollowEnabled,
    showShrimpFollowButton,
    shrimpPendingUpdateCount,
    shrimpFollowNotice,
    shrimpStreamStateText,
    shrimpAutoFollowButtonText,
    loadShrimpBrain,
    handleDeleteShrimpRun,
    connectShrimpBrainStream,
    closeShrimpBrainStream,
    setShrimpFromSnapshot,
    selectShrimpSession,
    shrimpFollowStatusLabel,
    handleShrimpEventStreamScroll,
    restoreShrimpAutoFollow,
    isShrimpReplyExpanded,
    toggleShrimpReply,
    isShrimpTurnExpanded,
    toggleShrimpTurn,
  };
}
