<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { fetchControlConfig, saveControlConfig } from "./api/controlConfig";
import { fetchDashboardSnapshot } from "./api/dashboard";
import { deleteShrimpBrainRun, fetchShrimpBrainSnapshot } from "./api/shrimpBrain";
import IconGlyph from "./components/IconGlyph.vue";
import SectionCard from "./components/SectionCard.vue";
import StatusPill from "./components/StatusPill.vue";
import {
  type ControlChannelConfig,
  type ControlConfig,
  type DashboardSnapshot,
  type ShrimpBrainLoopNode,
  type ShrimpBrainRun,
  type ShrimpBrainSnapshot,
  type ShrimpBrainToolCall,
  navStructure,
  pageMeta,
  type PageId,
} from "./data/dashboard";

interface ShrimpSessionGroup {
  blockKey: string;
  userKey: string;
  sessionKey: string;
  channel: string;
  chatId: string;
  mainAgentId: string;
  status: string;
  startedAt: number;
  updatedAt: number;
  runs: ShrimpBrainRun[];
}

const isSidebarOpen = ref(true);
const activePage = ref<PageId>("chat");
const snapshot = ref<DashboardSnapshot | null>(null);
const loading = ref(false);
const error = ref("");
const controlConfig = ref<ControlConfig | null>(null);
const controlDraft = ref<ControlConfig | null>(null);
const controlLoading = ref(false);
const controlSaving = ref(false);
const controlIssues = ref<string[]>([]);
const controlMessage = ref("");
const shrimpBrain = ref<ShrimpBrainSnapshot | null>(null);
const shrimpBrainLoading = ref(false);
const shrimpBrainError = ref("");
const shrimpBrainStreamState = ref("connecting");
const selectedShrimpSessionKey = ref("");
const deletingShrimpRunId = ref("");
const isUserInteracting = ref(false);
let refreshTimer: number | undefined;
let shrimpBrainStream: EventSource | null = null;
let interactionTimer: number | undefined;

const currentPage = computed(() => pageMeta[activePage.value]);

const chatAlerts = computed(() => snapshot.value?.chat.alerts ?? []);
const chatQuickActions = computed(() => snapshot.value?.chat.quickActions ?? []);
const overviewCards = computed(() => snapshot.value?.overview.cards ?? []);
const overviewPanels = computed(() => snapshot.value?.overview.panels ?? []);
const channels = computed(() => snapshot.value?.channels ?? []);
const instances = computed(() => snapshot.value?.instances ?? []);
const sessions = computed(() => snapshot.value?.sessions ?? []);
const cronJobs = computed(() => snapshot.value?.cronJobs ?? []);
const skills = computed(() => snapshot.value?.skills ?? []);
const nodes = computed(() => snapshot.value?.nodes ?? []);
const configGroups = computed(() => snapshot.value?.config.groups ?? []);
const configPreview = computed(() => snapshot.value?.config.preview ?? "");
const debugRows = computed(() => snapshot.value?.debug ?? []);
const logs = computed(() => snapshot.value?.logs ?? []);
const docs = computed(() => snapshot.value?.docs ?? []);
const shrimpRuns = computed(() => shrimpBrain.value?.runs ?? []);
const shrimpSessions = computed<ShrimpSessionGroup[]>(() => {
  const groups = new Map<string, ShrimpSessionGroup>();
  for (const run of shrimpRuns.value) {
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
});
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
const gatewayControl = computed(() => controlDraft.value?.gateway ?? null);
const controlChannels = computed(() => controlDraft.value?.channels ?? []);
const controlBindings = computed(() => controlDraft.value?.bindings ?? []);
const controlAgents = computed(() => controlDraft.value?.agents ?? []);
const generatedAtLabel = computed(() => {
  if (activePage.value === "shrimpBrain") {
    return shrimpBrainGeneratedAtLabel.value;
  }
  if (!snapshot.value?.generatedAt) {
    return "";
  }
  return new Date(snapshot.value.generatedAt).toLocaleString("zh-CN");
});
const controlGeneratedAtLabel = computed(() => {
  if (!controlConfig.value?.generatedAt) {
    return "";
  }
  return new Date(controlConfig.value.generatedAt).toLocaleString("zh-CN");
});
const isControlDirty = computed(() => {
  if (!controlConfig.value || !controlDraft.value) {
    return false;
  }
  return JSON.stringify(controlConfig.value) !== JSON.stringify(controlDraft.value);
});

const channelFieldSupport: Record<string, string[]> = {
  telegram: ["token"],
  whatsapp: ["bridgeUrl"],
  weixin: ["mode", "token", "baseUrl", "cdnBaseUrl", "proxy", "bridgeUrl"],
  imessage: ["bridgeUrl"],
  feishu: ["appId", "appSecret", "encryptKey", "verificationToken", "webhookPort"],
  qq: ["appId", "appSecret"],
  wework: [
    "mode",
    "corpId",
    "agentId",
    "secret",
    "botId",
    "botSecret",
    "webSocketUrl",
    "token",
    "encodingAESKey",
    "webhookPort",
  ],
  dingtalk: ["clientId", "clientSecret"],
  infoflow: ["webhookUrl", "token", "aesKey", "webhookPort"],
  gotify: ["serverUrl", "appToken", "priority"],
};

const fieldLabels: Record<string, string> = {
  token: "Token",
  baseUrl: "Base URL",
  cdnBaseUrl: "CDN Base URL",
  proxy: "Proxy",
  appId: "App ID",
  appSecret: "App Secret",
  corpId: "Corp ID",
  agentId: "Agent ID",
  secret: "Secret",
  botId: "Bot ID",
  botSecret: "Bot Secret",
  clientId: "Client ID",
  clientSecret: "Client Secret",
  bridgeUrl: "Bridge URL",
  webhookUrl: "Webhook URL",
  webSocketUrl: "WebSocket URL",
  aesKey: "AES Key",
  encodingAESKey: "Encoding AES Key",
  encryptKey: "Encrypt Key",
  verificationToken: "Verification Token",
  webhookPort: "Webhook Port",
  serverUrl: "Server URL",
  appToken: "App Token",
  priority: "Priority",
};

function normalizeWeWorkMode(mode: string) {
  const normalized = mode.trim().toLowerCase();
  return normalized || "webhook";
}

function normalizeWeixinMode(mode: string) {
  const normalized = mode.trim().toLowerCase();
  return normalized || "bridge";
}

function normalizeControlState(value: ControlConfig): ControlConfig {
  const next = JSON.parse(JSON.stringify(value)) as ControlConfig;
  next.channels = next.channels.map((item) => ({
    ...item,
    channel: item.channel.trim().toLowerCase(),
    accountId: item.accountId.trim(),
    mode:
      item.channel.trim().toLowerCase() === "wework"
        ? normalizeWeWorkMode(item.mode)
        : item.channel.trim().toLowerCase() === "weixin"
          ? normalizeWeixinMode(item.mode)
          : item.mode.trim(),
    secret: item.channel.trim().toLowerCase() === "wework" ? item.secret || item.appSecret : item.secret,
    allowedIds: Array.isArray(item.allowedIds) ? item.allowedIds : [],
  }));
  next.bindings = next.bindings.map((item) => ({
    ...item,
    channel: item.channel.trim().toLowerCase(),
    accountId: item.accountId.trim(),
    agentId: item.agentId.trim(),
  }));
  return next;
}

function isInteractiveElement(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  const tag = target.tagName.toLowerCase();
  return (
    tag === "input" ||
    tag === "textarea" ||
    tag === "select" ||
    tag === "button" ||
    target.isContentEditable
  );
}

function beginInteraction(timeoutMs = 6000) {
  isUserInteracting.value = true;
  if (interactionTimer) {
    window.clearTimeout(interactionTimer);
  }
  interactionTimer = window.setTimeout(() => {
    isUserInteracting.value = false;
  }, timeoutMs);
}

function endInteractionSoon() {
  if (interactionTimer) {
    window.clearTimeout(interactionTimer);
  }
  interactionTimer = window.setTimeout(() => {
    isUserInteracting.value = false;
  }, 800);
}

function shouldAutoRefresh() {
  if (document.hidden) {
    return false;
  }
  if (isUserInteracting.value || controlSaving.value || isControlDirty.value) {
    return false;
  }
  if (activePage.value === "config" || activePage.value === "channels") {
    return false;
  }
  return true;
}

async function loadSnapshot(options: { silent?: boolean } = {}) {
  const silent = options.silent === true;
  if (!silent || !snapshot.value) {
    loading.value = true;
  }
  if (!silent) {
    error.value = "";
  }

  try {
    snapshot.value = await fetchDashboardSnapshot();
    if (!shrimpBrain.value && snapshot.value?.shrimpBrain) {
      shrimpBrain.value = snapshot.value.shrimpBrain;
      syncSelectedShrimpRun();
    }
  } catch (err) {
    const message = err instanceof Error ? err.message : "failed to load dashboard";
    error.value = message;
  } finally {
    if (!silent || !snapshot.value) {
      loading.value = false;
    }
  }
}

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

function cloneControlState(value: ControlConfig): ControlConfig {
  return normalizeControlState(value);
}

async function loadControlState() {
  controlLoading.value = true;
  controlIssues.value = [];

  try {
    const nextConfig = normalizeControlState(await fetchControlConfig());
    controlConfig.value = nextConfig;
    controlDraft.value = cloneControlState(nextConfig);
  } catch (err) {
    const message = err instanceof Error ? err.message : "failed to load control config";
    controlIssues.value = [message];
  } finally {
    controlLoading.value = false;
  }
}

async function handleHeaderAction(_action: string) {
  await Promise.all([loadSnapshot(), loadShrimpBrain()]);
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

function handleWindowPointerDown() {
  beginInteraction(5000);
}

function handleWindowKeyDown() {
  beginInteraction(5000);
}

function handleWindowFocusIn(event: FocusEvent) {
  if (isInteractiveElement(event.target)) {
    beginInteraction(15000);
  }
}

function handleWindowFocusOut() {
  endInteractionSoon();
}

function handleVisibilityChange() {
  if (!document.hidden && shouldAutoRefresh()) {
    void loadSnapshot({ silent: true });
    if (shrimpBrainStreamState.value !== "live") {
      void loadShrimpBrain({ silent: true });
    }
  }
}

function shrimpStateLabel(value: string) {
  switch (value) {
    case "live":
      return "Live";
    case "connecting":
      return "Connecting";
    case "reconnecting":
      return "Reconnecting";
    case "unsupported":
      return "Unsupported";
    default:
      return value || "Idle";
  }
}

function shrimpStatusBucket(status: string) {
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

function shrimpStatusBadgeClass(status: string) {
  return `shrimp-status-badge is-${shrimpStatusBucket(status)}`;
}

function isFailureStatus(status: string) {
  return shrimpStatusBucket(status) === "failed";
}

function formatShrimpTimeShort(timestamp?: number) {
  if (!timestamp) return "";
  const d = new Date(timestamp);
  return `${d.getHours().toString().padStart(2, "0")}:${d.getMinutes().toString().padStart(2, "0")}:${d.getSeconds().toString().padStart(2, "0")}`;
}

function formatShrimpTime(timestamp?: number) {
  if (!timestamp) {
    return "";
  }
  return new Date(timestamp).toLocaleString("zh-CN");
}

function truncateShrimpText(value: string | undefined, limit = 20) {
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

function shrimpTurnTitle(index: number) {
  return `Turn ${index + 1}`;
}

function shrimpMainLoopTitle(loop: ShrimpBrainLoopNode) {
  return `主 Agent 外循环 ${loop.iteration}`;
}

function shrimpChildLoopTitle(loop: ShrimpBrainLoopNode) {
  return `子 Agent 内循环 ${loop.iteration}`;
}

function shrimpSessionPreview(session: ShrimpSessionGroup) {
  return truncateShrimpText(session.runs[0]?.userRequest, 20);
}

function shrimpSessionPathSummary(session: ShrimpSessionGroup) {
  return `channel:${session.channel || "cli"} / sender:${session.chatId || session.userKey || "unknown"}`;
}

function shrimpSessionPathFull(session: ShrimpSessionGroup) {
  return `${shrimpSessionPathSummary(session)} / block:${session.blockKey} / session:${session.sessionKey} / main:${session.mainAgentId || "main"}`;
}

function shrimpToolTitle(tool: ShrimpBrainToolCall) {
  if (tool.toolName === "sessions_spawn" && tool.childAgentId) {
    return `派发给 ${tool.childAgentId}`;
  }
  return tool.toolName;
}

function channelLabel(item: ControlChannelConfig) {
  return `${item.channel} / ${item.accountId || "all"}`;
}

function channelHint(item: ControlChannelConfig) {
  return item.legacy ? "Legacy single-account config" : "Multi-account config";
}

function supportsField(channel: string, field: string) {
  return channelFieldSupport[channel]?.includes(field) ?? false;
}

function currentWeWorkMode(item: ControlChannelConfig) {
  return item.channel === "wework" ? normalizeWeWorkMode(item.mode) : item.mode.trim().toLowerCase();
}

function currentWeixinMode(item: ControlChannelConfig) {
  return item.channel === "weixin" ? normalizeWeixinMode(item.mode) : item.mode.trim().toLowerCase();
}

function requiredFieldsForChannel(item: ControlChannelConfig) {
  switch (item.channel) {
    case "telegram":
      return ["token"];
    case "whatsapp":
    case "imessage":
      return ["bridgeUrl"];
    case "weixin":
      return currentWeixinMode(item) === "direct" ? ["token"] : ["bridgeUrl"];
    case "feishu":
      return ["appId", "appSecret"];
    case "qq":
      return ["appId", "appSecret"];
    case "wework":
      return currentWeWorkMode(item) === "websocket"
        ? ["botId", "botSecret"]
        : ["corpId", "agentId", "secret"];
    case "dingtalk":
      return ["clientId", "clientSecret"];
    case "infoflow":
      return ["webhookUrl", "token"];
    case "gotify":
      return ["serverUrl", "appToken"];
    default:
      return [];
  }
}

function channelRequiredSummary(item: ControlChannelConfig) {
  const labels = requiredFieldsForChannel(item).map((field) => fieldLabels[field] ?? field);
  return labels.length ? labels.join(" / ") : "No required fields";
}

function channelFieldValue(item: ControlChannelConfig, field: string) {
  switch (field) {
    case "token":
      return item.token;
    case "baseUrl":
      return item.baseUrl;
    case "cdnBaseUrl":
      return item.cdnBaseUrl;
    case "proxy":
      return item.proxy;
    case "appId":
      return item.appId;
    case "appSecret":
      return item.appSecret;
    case "corpId":
      return item.corpId;
    case "agentId":
      return item.agentId;
    case "secret":
      return item.secret || item.appSecret;
    case "botId":
      return item.botId;
    case "botSecret":
      return item.botSecret;
    case "clientId":
      return item.clientId;
    case "clientSecret":
      return item.clientSecret;
    case "bridgeUrl":
      return item.bridgeUrl;
    case "webhookUrl":
      return item.webhookUrl;
    case "serverUrl":
      return item.serverUrl;
    default:
      return "";
  }
}

function formatFieldList(fields: string[]) {
  return fields.map((field) => fieldLabels[field] ?? field).join(", ");
}

function missingRequiredFields(item: ControlChannelConfig) {
  return requiredFieldsForChannel(item).filter((field) => channelFieldValue(item, field).trim() === "");
}

function missingRequiredFieldSummary(item: ControlChannelConfig) {
  const missing = missingRequiredFields(item);
  return missing.length ? formatFieldList(missing) : "";
}

function isAbsoluteUrl(value: string) {
  try {
    const parsed = new URL(value.trim());
    return Boolean(parsed.protocol && parsed.host);
  } catch {
    return false;
  }
}

function isValidOptionalPort(value: number) {
  return value === 0 || (value >= 1024 && value <= 65535);
}

function validateControlDraft(value: ControlConfig) {
  const issues: string[] = [];

  if (value.gateway.webSocketAuthEnabled && !value.gateway.webSocketAuthToken.trim()) {
    issues.push("WebSocket Auth 开启后必须填写 WebSocket Token。");
  }

  for (const item of value.channels) {
    if (!item.enabled) {
      continue;
    }

    const missing = missingRequiredFields(item);
    if (missing.length > 0) {
      issues.push(`${channelLabel(item)} 缺少必填字段: ${formatFieldList(missing)}。`);
      continue;
    }

    if ((item.channel === "whatsapp" || item.channel === "imessage") && !isAbsoluteUrl(item.bridgeUrl)) {
      issues.push(`${channelLabel(item)} 的 Bridge URL 必须是完整 URL。`);
    }

    if (item.channel === "weixin") {
      if (currentWeixinMode(item) === "bridge") {
        if (!isAbsoluteUrl(item.bridgeUrl)) {
          issues.push(`${channelLabel(item)} 的 Bridge URL 必须是完整 URL。`);
        }
      } else {
        if (item.baseUrl.trim() && !isAbsoluteUrl(item.baseUrl)) {
          issues.push(`${channelLabel(item)} 的 Base URL 必须是完整 URL。`);
        }
        if (item.cdnBaseUrl.trim() && !isAbsoluteUrl(item.cdnBaseUrl)) {
          issues.push(`${channelLabel(item)} 的 CDN Base URL 必须是完整 URL。`);
        }
        if (item.proxy.trim() && !isAbsoluteUrl(item.proxy)) {
          issues.push(`${channelLabel(item)} 的 Proxy 必须是完整 URL。`);
        }
      }
    }

    if (item.channel === "infoflow" && !isAbsoluteUrl(item.webhookUrl)) {
      issues.push(`${channelLabel(item)} 的 Webhook URL 必须是完整 URL。`);
    }

    if (item.channel === "gotify" && !isAbsoluteUrl(item.serverUrl)) {
      issues.push(`${channelLabel(item)} 的 Server URL 必须是完整 URL。`);
    }

    if (supportsField(item.channel, "webhookPort") && !isValidOptionalPort(item.webhookPort)) {
      issues.push(`${channelLabel(item)} 的 Webhook Port 必须在 1024-65535 之间。`);
    }

    if (item.channel === "gotify" && item.priority !== 0 && (item.priority < 1 || item.priority > 10)) {
      issues.push(`${channelLabel(item)} 的 Priority 必须在 1-10 之间。`);
    }
  }

  return issues;
}

function allowedIdsText(item: ControlChannelConfig) {
  return item.allowedIds.join(", ");
}

function updateAllowedIds(item: ControlChannelConfig, rawValue: string) {
  item.allowedIds = rawValue
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean);
}

function onAllowedIdsInput(item: ControlChannelConfig, event: Event) {
  const target = event.target as HTMLInputElement | null;
  updateAllowedIds(item, target?.value ?? "");
}

function addBindingRow() {
  if (!controlDraft.value) {
    return;
  }
  const fallbackAgent =
    controlDraft.value.agents.find((item) => item.default)?.id
    ?? controlDraft.value.agents[0]?.id
    ?? "";
  controlDraft.value.bindings.push({
    channel: "",
    accountId: "",
    agentId: fallbackAgent,
  });
}

function removeBindingRow(index: number) {
  controlDraft.value?.bindings.splice(index, 1);
}

async function saveControlChanges() {
  if (!controlDraft.value) {
    return;
  }

  controlSaving.value = true;
  controlIssues.value = [];
  controlMessage.value = "";

  try {
    const payload = cloneControlState(controlDraft.value);
    payload.channels = payload.channels.map((item) => ({
      ...item,
      allowedIds: item.allowedIds.map((value) => value.trim()).filter(Boolean),
    }));
    payload.bindings = payload.bindings.map((item) => ({
      channel: item.channel.trim().toLowerCase(),
      accountId: item.accountId.trim(),
      agentId: item.agentId.trim(),
    }));

    const issues = validateControlDraft(payload);
    if (issues.length > 0) {
      controlIssues.value = issues;
      return;
    }

    const result = await saveControlConfig(payload);
    controlMessage.value = `${result.message} ${result.configPath}`;
    await Promise.all([loadSnapshot(), loadControlState()]);
  } catch (err) {
    const message = err instanceof Error ? err.message : "failed to save control config";
    controlIssues.value = [message];
  } finally {
    controlSaving.value = false;
  }
}

onMounted(() => {
  void Promise.all([loadSnapshot(), loadControlState(), loadShrimpBrain()]);
  connectShrimpBrainStream();
  window.addEventListener("pointerdown", handleWindowPointerDown, true);
  window.addEventListener("keydown", handleWindowKeyDown, true);
  window.addEventListener("focusin", handleWindowFocusIn, true);
  window.addEventListener("focusout", handleWindowFocusOut, true);
  document.addEventListener("visibilitychange", handleVisibilityChange);
  refreshTimer = window.setInterval(() => {
    if (!shouldAutoRefresh()) {
      return;
    }
    void loadSnapshot({ silent: true });
    if (shrimpBrainStreamState.value !== "live") {
      void loadShrimpBrain({ silent: true });
    }
  }, 30000);
});

onBeforeUnmount(() => {
  window.clearInterval(refreshTimer);
  if (interactionTimer) {
    window.clearTimeout(interactionTimer);
  }
  window.removeEventListener("pointerdown", handleWindowPointerDown, true);
  window.removeEventListener("keydown", handleWindowKeyDown, true);
  window.removeEventListener("focusin", handleWindowFocusIn, true);
  window.removeEventListener("focusout", handleWindowFocusOut, true);
  document.removeEventListener("visibilitychange", handleVisibilityChange);
  if (shrimpBrainStream) {
    shrimpBrainStream.close();
    shrimpBrainStream = null;
  }
});
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar" :class="{ collapsed: !isSidebarOpen }">
      <div class="sidebar-header">
        <button
          class="icon-button"
          type="button"
          aria-label="Toggle Sidebar"
          @click="isSidebarOpen = !isSidebarOpen"
        >
          <IconGlyph name="menu" :size="18" />
        </button>

        <div v-if="isSidebarOpen" class="brand-block">
          <div class="brand-icon">
            <IconGlyph name="bot" :size="22" />
          </div>
          <div class="brand-copy">
            <span>SUNCLAW</span>
            <small>CONTROL DASHBOARD</small>
          </div>
        </div>
      </div>

      <div class="sidebar-scroll">
        <section v-for="group in navStructure" :key="group.category" class="nav-group">
          <div v-if="isSidebarOpen" class="nav-group-head">
            <span>{{ group.category }}</span>
            <span>-</span>
          </div>

          <div class="nav-items">
            <button
              v-for="item in group.items"
              :key="item.id"
              class="nav-item"
              :class="{ active: activePage === item.id }"
              type="button"
              @click="activePage = item.id"
            >
              <IconGlyph :name="item.icon" :size="16" />
              <span v-if="isSidebarOpen">{{ item.label }}</span>
            </button>
          </div>
        </section>
      </div>
    </aside>

    <main class="main-panel">
      <div class="main-body" :class="{ 'main-body-shrimp': activePage === 'shrimpBrain' }">
        <header class="page-header" :class="{ 'page-header-tight': activePage === 'shrimpBrain' }">
          <div>
            <h1>{{ currentPage.title }}</h1>
            <p>{{ currentPage.subtitle }}</p>
            <button
              v-if="activePage === 'shrimpBrain'"
              class="secondary-button shrimp-refresh-stamp"
              type="button"
              :disabled="loading || shrimpBrainLoading"
              @click="handleHeaderAction('Refresh')"
            >
              <svg
                class="shrimp-refresh-icon"
                viewBox="0 0 24 24"
                fill="none"
                xmlns="http://www.w3.org/2000/svg"
                aria-hidden="true"
              >
                <path
                  d="M20 4v6h-6"
                  stroke="currentColor"
                  stroke-width="1.8"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
                <path
                  d="M20 10a8 8 0 1 0 2 5.3"
                  stroke="currentColor"
                  stroke-width="1.8"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </svg>
              <span>{{ generatedAtLabel ? `Last updated ${generatedAtLabel}` : "Refresh snapshot" }}</span>
            </button>
            <small v-else-if="generatedAtLabel" class="page-meta">Last updated {{ generatedAtLabel }}</small>
          </div>

          <div class="page-actions" v-if="currentPage.actions?.length && activePage !== 'shrimpBrain'">
            <button
              v-for="action in currentPage.actions"
              :key="action"
              class="secondary-button"
              type="button"
              @click="handleHeaderAction(action)"
            >
              {{ action }}
            </button>
          </div>
        </header>

        <div v-if="loading" class="runtime-banner tone-sky">
          <p>Loading live dashboard snapshot...</p>
        </div>

        <div v-else-if="error" class="runtime-banner tone-rose">
          <p>Failed to load live dashboard data: {{ error }}</p>
        </div>

        <template v-if="activePage === 'chat'">
          <div class="page-stack">
            <div class="alert-stack">
              <div
                v-for="alert in chatAlerts"
                :key="alert.text"
                class="alert-box"
                :class="`tone-${alert.tone}`"
              >
                <p>{{ alert.text }}</p>
              </div>
            </div>

            <SectionCard
              eyebrow="Quick Actions"
              title="Gateway interventions"
              note="围绕 SunClaw 网关做轻量控制入口"
            >
              <div class="action-grid">
                <article
                  v-for="item in chatQuickActions"
                  :key="item.label"
                  class="action-card"
                >
                  <strong>{{ item.label }}</strong>
                  <p>{{ item.note }}</p>
                </article>
              </div>
            </SectionCard>
          </div>
        </template>

        <template v-else-if="activePage === 'shrimpBrain'">
          <div class="page-stack shrimp-page">
            <section class="shrimp-top-strip">
              <div class="shrimp-top-chip">
                <span>团队</span>
                <strong>{{ shrimpBrain?.teamName || "虾脑" }}</strong>
              </div>
              <div class="shrimp-inline-metric">
                <span>活跃任务</span>
                <strong>{{ shrimpBrain?.activeRuns ?? 0 }}</strong>
              </div>
              <div class="shrimp-inline-metric">
                <span>流状态</span>
                <span :class="shrimpStatusBadgeClass(shrimpBrainStreamState)">{{ shrimpStateLabel(shrimpBrainStreamState) }}</span>
              </div>
            </section>

            <div v-if="shrimpBrainLoading" class="runtime-banner tone-sky">
              <p>Loading shrimp brain snapshot...</p>
            </div>

            <div v-else-if="shrimpBrainError" class="runtime-banner tone-rose">
              <p>Failed to load shrimp brain: {{ shrimpBrainError }}</p>
            </div>

            <div v-else-if="shrimpBrain && !shrimpBrain.available" class="runtime-banner tone-amber">
              <p>{{ shrimpBrain.note || "Shrimp brain is unavailable in the current runtime." }}</p>
            </div>

            <div v-else class="shrimp-layout shrimp-layout-expanded">
              <SectionCard
                class="shrimp-panel shrimp-queue-panel"
                eyebrow="会话队列"
                title="任务队列"
                note="按会话聚合，点击切换当前链路"
              >
                <div class="shrimp-queue-cards">
                  <button
                    v-for="session in shrimpSessions"
                    :key="session.blockKey"
                    class="shrimp-queue-card"
                    :class="{ active: selectedShrimpSession?.blockKey === session.blockKey }"
                    type="button"
                    @click="selectedShrimpSessionKey = session.blockKey"
                  >
                    <div class="shrimp-queue-card-head">
                      <div class="shrimp-queue-agent-block">
                        <span class="shrimp-queue-label">Agent ID</span>
                        <strong class="shrimp-queue-agent">{{ session.mainAgentId || "main" }}</strong>
                      </div>
                      <span :class="shrimpStatusBadgeClass(session.status)">{{ session.status }}</span>
                    </div>

                    <div class="shrimp-queue-card-body">
                      <div class="shrimp-queue-main">
                        <span class="shrimp-queue-label">首条用户输入</span>
                        <p
                          class="shrimp-queue-request"
                          :title="session.runs[0]?.userRequest || ''"
                        >
                          {{ shrimpSessionPreview(session) }}
                        </p>
                      </div>

                      <div class="shrimp-queue-meta-grid">
                        <div class="shrimp-queue-meta-item">
                          <span class="shrimp-queue-label">开始时间</span>
                          <strong :title="formatShrimpTime(session.startedAt)">{{ formatShrimpTimeShort(session.startedAt) }}</strong>
                        </div>
                        <div class="shrimp-queue-meta-item">
                          <span class="shrimp-queue-label">渠道</span>
                          <strong>{{ session.channel || "cli" }}</strong>
                        </div>
                        <div class="shrimp-queue-meta-item">
                          <span class="shrimp-queue-label">轮数</span>
                          <strong>{{ session.runs.length }}</strong>
                        </div>
                      </div>
                    </div>
                  </button>
                  <div v-if="!shrimpSessions.length" class="shrimp-empty-state">暂无任务队列数据</div>
                </div>
              </SectionCard>

              <SectionCard
                v-if="selectedShrimpSession"
                class="shrimp-panel shrimp-detail-panel"
                eyebrow="团队协作"
                title="会话全链路"
              >
                <div class="section-stack shrimp-section-stack">
                  <div class="shrimp-session-inline">
                    <div class="shrimp-summary-card">
                      <span>主 Agent</span>
                      <strong>{{ selectedShrimpSession.mainAgentId || "main" }}</strong>
                    </div>
                    <div class="shrimp-summary-card">
                      <span>Sender</span>
                      <strong>{{ selectedShrimpSession.chatId || selectedShrimpSession.userKey || "-" }}</strong>
                    </div>
                    <div class="shrimp-summary-card">
                      <span>状态</span>
                      <span :class="shrimpStatusBadgeClass(selectedShrimpSession.status)">{{ selectedShrimpSession.status }}</span>
                    </div>
                    <div class="shrimp-summary-card">
                      <span>开始时间</span>
                      <strong>{{ formatShrimpTime(selectedShrimpSession.startedAt) }}</strong>
                    </div>
                  </div>

                  <details class="shrimp-inline-details shrimp-session-path">
                    <summary>
                      <span>Session 路径</span>
                      <code>{{ shrimpSessionPathSummary(selectedShrimpSession) }}</code>
                    </summary>
                    <div class="shrimp-inline-body">
                      <code>{{ shrimpSessionPathFull(selectedShrimpSession) }}</code>
                    </div>
                  </details>

                  <div class="shrimp-turn-list">
                    <template v-for="(run, runIndex) in selectedShrimpSession.runs" :key="run.id">
                      <article class="shrimp-turn-card" :class="{ failed: isFailureStatus(run.status) }">
                        <div class="shrimp-turn-toolbar">
                          <div class="shrimp-turn-meta">
                            <strong>{{ shrimpTurnTitle(runIndex) }}</strong>
                            <span :class="shrimpStatusBadgeClass(run.status)">{{ run.status }}</span>
                            <span class="shrimp-turn-time" :title="formatShrimpTime(run.startedAt)">{{ formatShrimpTimeShort(run.startedAt) }}</span>
                          </div>
                          <button
                            class="danger-button shrimp-delete-button"
                            type="button"
                            :disabled="deletingShrimpRunId === run.id"
                            @click="handleDeleteShrimpRun(run.id)"
                          >
                            {{ deletingShrimpRunId === run.id ? "Deleting..." : "删除此轮" }}
                          </button>
                        </div>

                        <div class="shrimp-primary-block">
                          <div class="shrimp-block-head">
                            <strong>用户输入</strong>
                            <span class="shrimp-inline-key">mainAgentId: {{ run.mainAgentId }}</span>
                          </div>
                          <p>{{ run.userRequest }}</p>
                        </div>

                        <details v-if="run.mainPrompt" class="shrimp-collapsible">
                          <summary>
                            <span>主 Agent 提示词</span>
                            <small>{{ run.mainLayers?.length ?? 0 }} layers</small>
                          </summary>
                          <small v-if="run.mainPromptAt" class="helper-text">{{ formatShrimpTime(run.mainPromptAt) }}</small>
                          <div v-if="run.mainLayers?.length" class="shrimp-chip-row">
                            <span v-for="layer in run.mainLayers" :key="`${run.id}-${layer}`">{{ layer }}</span>
                          </div>
                          <pre>{{ run.mainPrompt }}</pre>
                        </details>

                        <details v-if="run.mainLoops?.length" class="shrimp-collapsible">
                          <summary>
                            <span>mainLoops</span>
                            <small>{{ run.mainLoops.length }}</small>
                          </summary>
                          <div class="shrimp-secondary-stack">
                            <article
                              v-for="loop in run.mainLoops"
                              :key="loop.id"
                              class="shrimp-secondary-card"
                              :class="{ failed: isFailureStatus(loop.status) }"
                            >
                              <div class="shrimp-secondary-head">
                                <div>
                                  <strong>{{ shrimpMainLoopTitle(loop) }}</strong>
                                  <p>{{ loop.summary || loop.reply || "本轮暂无摘要" }}</p>
                                </div>
                                <span :class="shrimpStatusBadgeClass(loop.stopReason || loop.status)">{{ loop.stopReason || loop.status }}</span>
                              </div>
                              <div class="shrimp-secondary-meta">
                                <span>agentId: {{ loop.agentId }}</span>
                                <span>{{ formatShrimpTime(loop.updatedAt) }}</span>
                              </div>

                              <details v-if="loop.reply" class="shrimp-inline-details">
                                <summary>本轮模型回复</summary>
                                <pre>{{ loop.reply }}</pre>
                              </details>

                              <details v-if="loop.toolCalls?.length" class="shrimp-inline-details">
                                <summary>
                                  <span>toolCalls</span>
                                  <small>{{ loop.toolCalls.length }}</small>
                                </summary>
                                <div class="shrimp-tool-stack">
                                  <article
                                    v-for="tool in loop.toolCalls"
                                    :key="tool.id || `${loop.id}-${tool.toolName}`"
                                    class="shrimp-tool-row"
                                    :class="{ failed: isFailureStatus(tool.childStatus || tool.status) }"
                                  >
                                    <div class="shrimp-secondary-head">
                                      <div>
                                        <strong>{{ shrimpToolTitle(tool) }}</strong>
                                        <p>{{ tool.summary || tool.task || tool.result || tool.error || "工具调用" }}</p>
                                      </div>
                                      <span :class="shrimpStatusBadgeClass(tool.childStatus || tool.status)">
                                        {{ tool.childAgentId || tool.status }}
                                      </span>
                                    </div>
                                    <div class="shrimp-secondary-meta">
                                      <span>{{ formatShrimpTime(tool.updatedAt) }}</span>
                                      <span v-if="tool.childAgentId">child: {{ tool.childAgentId }}</span>
                                    </div>

                                    <details v-if="tool.task" class="shrimp-inline-details">
                                      <summary>派发任务</summary>
                                      <pre>{{ tool.task }}</pre>
                                    </details>

                                    <details v-if="tool.arguments" class="shrimp-inline-details">
                                      <summary>工具参数</summary>
                                      <pre>{{ tool.arguments }}</pre>
                                    </details>

                                    <details v-if="tool.result" class="shrimp-inline-details">
                                      <summary>工具结果</summary>
                                      <pre>{{ tool.result }}</pre>
                                    </details>

                                    <details v-if="tool.error" class="shrimp-inline-details danger">
                                      <summary>工具错误</summary>
                                      <pre>{{ tool.error }}</pre>
                                    </details>

                                    <details v-if="tool.childPrompt" class="shrimp-inline-details">
                                      <summary>
                                        <span>子 Agent 提示词</span>
                                        <small>{{ tool.childPromptLayers?.length ?? 0 }} layers</small>
                                      </summary>
                                      <small class="helper-text">{{ formatShrimpTime(tool.updatedAt) }}</small>
                                      <div v-if="tool.childPromptLayers?.length" class="shrimp-chip-row">
                                        <span v-for="layer in tool.childPromptLayers" :key="`${tool.id}-${layer}`">{{ layer }}</span>
                                      </div>
                                      <pre>{{ tool.childPrompt }}</pre>
                                    </details>

                                    <details v-if="tool.childReply" class="shrimp-inline-details">
                                      <summary>子 Agent 回复</summary>
                                      <small class="helper-text">{{ formatShrimpTime(tool.updatedAt) }}</small>
                                      <pre>{{ tool.childReply }}</pre>
                                    </details>

                                    <details v-if="tool.childLoops?.length" class="shrimp-inline-details">
                                      <summary>
                                        <span>childLoops</span>
                                        <small>{{ tool.childLoops.length }}</small>
                                      </summary>
                                      <div class="shrimp-secondary-stack">
                                        <article
                                          v-for="childLoop in tool.childLoops"
                                          :key="childLoop.id"
                                          class="shrimp-secondary-card child"
                                          :class="{ failed: isFailureStatus(childLoop.status) }"
                                        >
                                          <div class="shrimp-secondary-head">
                                            <div>
                                              <strong>{{ shrimpChildLoopTitle(childLoop) }}</strong>
                                              <p>{{ childLoop.summary || childLoop.reply || "本轮暂无摘要" }}</p>
                                            </div>
                                            <span :class="shrimpStatusBadgeClass(childLoop.stopReason || childLoop.status)">
                                              {{ childLoop.stopReason || childLoop.status }}
                                            </span>
                                          </div>
                                          <div class="shrimp-secondary-meta">
                                            <span>agentId: {{ childLoop.agentId }}</span>
                                            <span>{{ formatShrimpTime(childLoop.updatedAt) }}</span>
                                          </div>

                                          <details v-if="childLoop.reply" class="shrimp-inline-details">
                                            <summary>子 Agent 本轮回复</summary>
                                            <pre>{{ childLoop.reply }}</pre>
                                          </details>

                                          <details v-if="childLoop.toolCalls?.length" class="shrimp-inline-details">
                                            <summary>
                                              <span>toolCalls</span>
                                              <small>{{ childLoop.toolCalls.length }}</small>
                                            </summary>
                                            <div class="shrimp-tool-stack">
                                              <article
                                                v-for="childTool in childLoop.toolCalls"
                                                :key="childTool.id || `${childLoop.id}-${childTool.toolName}`"
                                                class="shrimp-tool-row compact"
                                                :class="{ failed: isFailureStatus(childTool.status) }"
                                              >
                                                <div class="shrimp-secondary-head">
                                                  <div>
                                                    <strong>{{ childTool.toolName }}</strong>
                                                    <p>{{ childTool.summary || childTool.result || childTool.error || "工具调用" }}</p>
                                                  </div>
                                                  <span :class="shrimpStatusBadgeClass(childTool.status)">{{ childTool.status }}</span>
                                                </div>
                                                <div class="shrimp-secondary-meta">
                                                  <span>{{ formatShrimpTime(childTool.updatedAt) }}</span>
                                                </div>
                                              </article>
                                            </div>
                                          </details>
                                        </article>
                                      </div>
                                    </details>
                                  </article>
                                </div>
                              </details>
                            </article>
                          </div>
                        </details>

                        <div v-if="run.mainReply" class="shrimp-primary-block shrimp-reply-block">
                          <div class="shrimp-block-head">
                            <strong>主 Agent 最终回复</strong>
                            <small class="helper-text">{{ formatShrimpTime(run.mainReplyAt) }}</small>
                          </div>
                          <pre>{{ run.mainReply }}</pre>
                        </div>
                      </article>

                      <div
                        v-if="runIndex < selectedShrimpSession.runs.length - 1"
                        class="shrimp-transition-card"
                      >
                        <strong>中止等待用户确认</strong>
                        <p>
                          下一轮用户继续输入：
                          {{ selectedShrimpSession.runs[runIndex + 1]?.userRequest }}
                        </p>
                      </div>
                    </template>
                  </div>
                </div>
              </SectionCard>
            </div>
          </div>
        </template>

        <template v-else-if="activePage === 'instances'">
          <SectionCard eyebrow="Gateway" title="Instances">
            <div class="card-grid three">
              <article v-for="item in instances" :key="item.name" class="info-card">
                <div class="info-head">
                  <strong>{{ item.name }}</strong>
                  <StatusPill :label="item.status" :tone="item.tone" />
                </div>
                <p class="muted">{{ item.kind }}</p>
                <code>{{ item.endpoint }}</code>
                <dl class="meta-list">
                  <div>
                    <dt>Auth</dt>
                    <dd>{{ item.auth }}</dd>
                  </div>
                  <div>
                    <dt>Last Seen</dt>
                    <dd>{{ item.lastSeen }}</dd>
                  </div>
                </dl>
              </article>
            </div>
          </SectionCard>
        </template>

        <template v-else-if="activePage === 'sessions'">
          <SectionCard eyebrow="Runtime" title="Sessions">
            <div class="table-wrap">
              <div class="table-head six">
                <span>Session Key</span>
                <span>Agent</span>
                <span>Channel</span>
                <span>Messages</span>
                <span>Updated</span>
                <span>State</span>
              </div>
              <div v-for="item in sessions" :key="item.key" class="table-row six">
                <code>{{ item.key }}</code>
                <strong>{{ item.agent }}</strong>
                <span>{{ item.channel }}</span>
                <span>{{ item.messages }}</span>
                <span>{{ item.updatedAt }}</span>
                <StatusPill :label="item.state" :tone="item.tone" />
              </div>
            </div>
          </SectionCard>
        </template>

        <template v-else-if="activePage === 'cron'">
          <SectionCard eyebrow="Automation" title="Cron Jobs">
            <div class="table-wrap">
              <div class="table-head six">
                <span>Name</span>
                <span>Schedule</span>
                <span>Target</span>
                <span>Delivery</span>
                <span>Next Run</span>
                <span>State</span>
              </div>
              <div v-for="item in cronJobs" :key="item.name" class="table-row six">
                <strong>{{ item.name }}</strong>
                <span>{{ item.schedule }}</span>
                <code>{{ item.target }}</code>
                <span>{{ item.delivery }}</span>
                <span>{{ item.nextRun }}</span>
                <StatusPill :label="item.state" :tone="item.tone" />
              </div>
            </div>
          </SectionCard>
        </template>

        <template v-else-if="activePage === 'skills'">
          <SectionCard eyebrow="Agent" title="Skills">
            <div class="table-wrap">
              <div class="table-head five">
                <span>Name</span>
                <span>Source</span>
                <span>Requires</span>
                <span>Scope</span>
                <span>State</span>
              </div>
              <div v-for="item in skills" :key="item.name" class="table-row five">
                <strong>{{ item.name }}</strong>
                <span>{{ item.source }}</span>
                <span>{{ item.requires }}</span>
                <span>{{ item.scope }}</span>
                <StatusPill :label="item.state" :tone="item.tone" />
              </div>
            </div>
          </SectionCard>
        </template>

        <template v-else-if="activePage === 'nodes'">
          <SectionCard eyebrow="Agent" title="Nodes">
            <div class="card-grid two">
              <article v-for="item in nodes" :key="item.name" class="info-card">
                <div class="info-head">
                  <strong>{{ item.name }}</strong>
                  <StatusPill :label="item.state" :tone="item.tone" />
                </div>
                <p class="muted">{{ item.role }} · {{ item.provider }}</p>
                <code>{{ item.workspace }}</code>
                <div class="chip-row">
                  <span v-for="tool in item.tools" :key="tool" class="chip">{{ tool }}</span>
                </div>
              </article>
            </div>
          </SectionCard>
        </template>

        <template v-else-if="activePage === 'config'">
          <div class="page-stack">
            <SectionCard eyebrow="Settings" title="Config Groups">
              <div class="card-grid three">
                <article v-for="group in configGroups" :key="group.title" class="info-card">
                  <strong>{{ group.title }}</strong>
                  <dl class="meta-list compact">
                    <div v-for="field in group.fields" :key="field.label">
                      <dt>{{ field.label }}</dt>
                      <dd>{{ field.value }}</dd>
                    </div>
                  </dl>
                </article>
              </div>
            </SectionCard>

            <SectionCard eyebrow="Preview" title="config.yaml excerpt">
              <pre class="code-preview"><code>{{ configPreview }}</code></pre>
            </SectionCard>
          </div>
        </template>

        <template v-else-if="activePage === 'debug'">
          <SectionCard eyebrow="Settings" title="Debug">
            <div class="card-grid three">
              <article v-for="item in debugRows" :key="item.title" class="info-card">
                <div class="info-head">
                  <strong>{{ item.title }}</strong>
                  <StatusPill :label="item.state" :tone="item.tone" />
                </div>
                <p>{{ item.description }}</p>
              </article>
            </div>
          </SectionCard>
        </template>

        <template v-else-if="activePage === 'logs'">
          <SectionCard eyebrow="Settings" title="Logs">
            <div class="log-list">
              <article v-for="item in logs" :key="`${item.time}-${item.origin}`" class="log-row">
                <div class="log-meta">
                  <span>{{ item.time }}</span>
                  <StatusPill :label="item.level" :tone="item.tone" />
                  <strong>{{ item.origin }}</strong>
                </div>
                <p>{{ item.message }}</p>
              </article>
            </div>
          </SectionCard>
        </template>

        <template v-else-if="activePage === 'docs'">
          <SectionCard eyebrow="Resources" title="Docs">
            <div class="card-grid two">
              <article v-for="item in docs" :key="item.path" class="info-card">
                <strong>{{ item.title }}</strong>
                <code>{{ item.path }}</code>
                <p>{{ item.description }}</p>
              </article>
            </div>
          </SectionCard>
        </template>
      </div>
    </main>
  </div>
</template>

<style scoped>
.main-body-shrimp {
  padding: 18px 20px 20px;
  width: 100%;
  max-width: none;
}

.page-header-tight {
  margin-bottom: 10px;
}

.shrimp-refresh-stamp {
  gap: 6px;
  margin-top: 8px;
  padding: 7px 10px;
  font-size: 12px;
}

.shrimp-refresh-icon {
  width: 13px;
  height: 13px;
  flex: none;
}

.shrimp-page {
  gap: 10px;
}

.shrimp-page .runtime-banner {
  margin-bottom: 0;
}

.shrimp-top-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.shrimp-top-chip,
.shrimp-inline-metric {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
  padding: 5px 10px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--panel);
}

.shrimp-top-chip > span,
.shrimp-inline-metric > span:first-child {
  color: var(--muted);
  font-size: 11px;
  white-space: nowrap;
}

.shrimp-top-chip strong,
.shrimp-inline-metric strong {
  font-size: 12px;
}

/* 虾脑布局 - 铺满屏幕 */
.shrimp-layout-expanded {
  display: grid;
  grid-template-columns: minmax(400px, 1fr) minmax(0, 2fr);
  gap: 16px;
  align-items: start;
  height: calc(100vh - 140px);
}

.shrimp-layout-expanded .shrimp-panel {
  height: 100%;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.shrimp-layout-expanded .shrimp-panel :deep(.section-body) {
  flex: 1;
  overflow-y: auto;
  padding: 12px 14px 14px;
}

/* 会话队列面板优化 */
.shrimp-queue-panel :deep(.section-head) {
  gap: 8px;
  padding: 12px 14px 0;
  flex-shrink: 0;
}

.shrimp-queue-panel :deep(.section-head h3) {
  font-size: 15px;
}

.shrimp-queue-panel :deep(.section-note) {
  font-size: 10px;
}

/* 会话详情面板优化 */
.shrimp-detail-panel :deep(.section-head) {
  gap: 8px;
  padding: 12px 14px 0;
  flex-shrink: 0;
}

.shrimp-detail-panel :deep(.section-head h3) {
  font-size: 15px;
}

.shrimp-detail-panel :deep(.section-note) {
  font-size: 10px;
}

.shrimp-detail-panel :deep(.section-body) {
  padding: 12px 14px 14px;
}

.shrimp-layout {
  display: grid;
  grid-template-columns: minmax(320px, 380px) minmax(0, 1fr);
  gap: 12px;
  align-items: start;
}

.shrimp-panel :deep(.section-head) {
  gap: 8px;
  padding: 10px 12px 0;
}

.shrimp-panel :deep(.section-head h3) {
  font-size: 15px;
}

.shrimp-panel :deep(.section-note) {
  font-size: 10px;
}

.shrimp-panel :deep(.section-body) {
  padding: 10px 12px 12px;
}

.shrimp-queue-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 10px;
}

.shrimp-queue-card {
  position: relative;
  display: grid;
  gap: 10px;
  padding: 12px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--panel);
  text-align: left;
  color: var(--text);
  cursor: pointer;
  transition:
    transform 160ms ease,
    border-color 160ms ease,
    background 160ms ease,
    box-shadow 160ms ease;
}

.shrimp-queue-card::before,
.shrimp-turn-card::before {
  content: "";
  position: absolute;
  left: 0;
  width: 3px;
  border-radius: 0 3px 3px 0;
  background: transparent;
}

.shrimp-queue-card::before {
  top: 10px;
  bottom: 10px;
}

.shrimp-queue-card:hover {
  transform: translateY(-1px);
  border-color: rgba(96, 165, 250, 0.24);
  background: var(--panel-soft);
  box-shadow: 0 10px 24px rgba(15, 23, 42, 0.08);
}

.shrimp-queue-card.active {
  border-color: rgba(37, 99, 235, 0.28);
  background: var(--accent-soft);
  box-shadow: 0 12px 28px rgba(37, 99, 235, 0.12);
}

.shrimp-queue-card.active::before {
  background: var(--accent-strong);
}

.shrimp-queue-card-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.shrimp-queue-card-body {
  display: grid;
  gap: 10px;
}

.shrimp-queue-agent-block,
.shrimp-queue-main,
.shrimp-queue-meta-item {
  display: grid;
  gap: 4px;
  min-width: 0;
}

.shrimp-queue-label {
  color: var(--muted-soft);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.shrimp-queue-agent {
  font-size: 13px;
  line-height: 1.3;
}

.shrimp-queue-request {
  margin: 0;
  overflow: hidden;
  color: var(--text);
  font-size: 12px;
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.shrimp-queue-meta-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}

.shrimp-queue-meta-item strong {
  font-size: 12px;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.shrimp-empty-state {
  padding: 10px 0 2px;
  color: var(--muted-soft);
  font-size: 11px;
}

.shrimp-section-stack {
  gap: 8px;
}

.shrimp-session-inline {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
}

.shrimp-summary-card,
.shrimp-primary-block,
.shrimp-collapsible,
.shrimp-inline-details,
.shrimp-turn-card,
.shrimp-secondary-card,
.shrimp-tool-row {
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--panel);
}

.shrimp-summary-card {
  padding: 10px 12px;
}

.shrimp-summary-card > span {
  display: block;
  margin-bottom: 4px;
  color: var(--muted);
  font-size: 11px;
}

.shrimp-summary-card strong {
  font-size: 13px;
}

.shrimp-turn-list {
  display: grid;
  gap: 8px;
}

.shrimp-turn-card {
  position: relative;
  display: grid;
  gap: 8px;
  padding: 10px 12px;
  background: var(--panel);
}

.shrimp-turn-card::before {
  top: 10px;
  bottom: 10px;
}

.shrimp-turn-card.failed::before {
  background: #ef4444;
}

.shrimp-turn-toolbar,
.shrimp-block-head,
.shrimp-secondary-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.shrimp-turn-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.shrimp-turn-meta strong {
  font-size: 13px;
}

.shrimp-turn-time,
.shrimp-secondary-meta {
  color: var(--muted-soft);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}

.shrimp-delete-button {
  padding: 6px 8px;
  border-radius: 7px;
  font-size: 11px;
  opacity: 0;
  visibility: hidden;
  pointer-events: none;
  transition: opacity 160ms ease;
}

.shrimp-turn-card:hover .shrimp-delete-button,
.shrimp-turn-card:focus-within .shrimp-delete-button {
  opacity: 1;
  visibility: visible;
  pointer-events: auto;
}

.shrimp-primary-block {
  padding: 10px 12px;
}

.shrimp-inline-key {
  color: var(--muted);
  font-size: 10px;
  white-space: nowrap;
}

.shrimp-primary-block p,
.shrimp-secondary-head p {
  margin: 0;
  line-height: 1.45;
}

.shrimp-primary-block p {
  color: var(--text);
  font-size: 12px;
}

.shrimp-reply-block {
  background: var(--accent-soft);
  border-color: rgba(37, 99, 235, 0.2);
}

.shrimp-reply-block pre,
.shrimp-collapsible pre,
.shrimp-inline-details pre {
  margin: 6px 0 0;
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.45;
  font-size: 11px;
  color: var(--text);
}

.shrimp-collapsible,
.shrimp-inline-details {
  padding: 0 10px;
}

.shrimp-collapsible[open],
.shrimp-inline-details[open] {
  padding-bottom: 10px;
}

.shrimp-collapsible summary,
.shrimp-inline-details summary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
  cursor: pointer;
  list-style: none;
  color: var(--text);
  font-size: 11px;
  font-weight: 600;
}

.shrimp-collapsible summary::-webkit-details-marker,
.shrimp-inline-details summary::-webkit-details-marker {
  display: none;
}

.shrimp-collapsible summary code,
.shrimp-collapsible summary small,
.shrimp-inline-details summary code,
.shrimp-inline-details summary small {
  margin-left: auto;
  color: var(--muted);
  font-size: 10px;
  font-weight: 600;
}

.shrimp-session-path summary code {
  max-width: calc(100% - 104px);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.shrimp-inline-body {
  padding-top: 2px;
}

.shrimp-inline-body code {
  display: block;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--muted);
  font-size: 11px;
}

.shrimp-collapsible .helper-text,
.shrimp-inline-details .helper-text {
  display: block;
  margin-top: 2px;
  font-size: 10px;
}

.shrimp-chip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 6px;
}

.shrimp-chip-row span {
  padding: 2px 6px;
  border-radius: 999px;
  background: var(--slate-soft);
  color: var(--slate);
  font-size: 10px;
}

.shrimp-secondary-stack,
.shrimp-tool-stack {
  display: grid;
  gap: 8px;
  margin-top: 8px;
}

.shrimp-secondary-card,
.shrimp-tool-row {
  display: grid;
  gap: 6px;
  padding: 10px 12px;
  background: var(--panel);
}

.shrimp-secondary-card.failed,
.shrimp-tool-row.failed,
.shrimp-inline-details.danger {
  border-color: #fecaca;
  background: #fff7f7;
}

.shrimp-secondary-head > div {
  min-width: 0;
}

.shrimp-secondary-head strong {
  display: block;
  font-size: 12px;
}

.shrimp-secondary-head p {
  margin-top: 3px;
  color: var(--muted);
  font-size: 11px;
}

.shrimp-secondary-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.shrimp-transition-card {
  padding: 10px 12px;
  border-left: 3px solid var(--amber);
  border-radius: 0 10px 10px 0;
  background: var(--amber-soft);
}

.shrimp-transition-card strong {
  display: block;
  font-size: 12px;
}

.shrimp-transition-card p {
  margin: 4px 0 0;
  color: var(--muted);
  line-height: 1.45;
  font-size: 11px;
}

.shrimp-status-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 18px;
  padding: 0 7px;
  border-radius: 999px;
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
  white-space: nowrap;
}

.shrimp-status-badge.is-completed {
  color: var(--emerald);
  background: var(--emerald-soft);
}

.shrimp-status-badge.is-running {
  color: var(--sky);
  background: var(--sky-soft);
}

.shrimp-status-badge.is-failed {
  color: #dc2626;
  background: #fee2e2;
}

.shrimp-status-badge.is-pending {
  color: var(--slate);
  background: var(--slate-soft);
}

@media (max-width: 1400px) {
  .shrimp-layout-expanded {
    grid-template-columns: minmax(360px, 1fr) minmax(0, 1.5fr);
  }
}

@media (max-width: 1180px) {
  .main-body-shrimp {
    padding: 16px 16px 18px;
  }

  .shrimp-layout {
    grid-template-columns: 1fr;
  }
  
  .shrimp-layout-expanded {
    grid-template-columns: 1fr;
    height: auto;
  }
  
  .shrimp-layout-expanded .shrimp-panel {
    height: auto;
    max-height: 60vh;
  }
}

@media (max-width: 900px) {
  .main-body-shrimp {
    padding: 14px 14px 16px;
  }

  .shrimp-session-inline {
    grid-template-columns: 1fr;
  }

  .shrimp-top-strip {
    flex-direction: column;
  }

  .shrimp-top-chip,
  .shrimp-inline-metric {
    justify-content: space-between;
  }
}

@media (max-width: 760px) {
  .main-body-shrimp {
    padding: 12px 12px 14px;
  }

  .shrimp-panel :deep(.section-head) {
    flex-direction: column;
  }

  .shrimp-queue-cards {
    grid-template-columns: 1fr;
  }

  .shrimp-queue-card-head,
  .shrimp-session-path summary {
    flex-direction: column;
    align-items: flex-start;
  }

  .shrimp-queue-meta-grid {
    grid-template-columns: 1fr;
  }

  .shrimp-session-path summary code {
    max-width: 100%;
    margin-left: 0;
  }

  .shrimp-turn-toolbar,
  .shrimp-block-head,
  .shrimp-secondary-head {
    flex-direction: column;
  }

  .shrimp-delete-button {
    opacity: 1;
    visibility: visible;
    pointer-events: auto;
    align-self: flex-start;
  }

  .shrimp-inline-key {
    white-space: normal;
  }
}
</style>
