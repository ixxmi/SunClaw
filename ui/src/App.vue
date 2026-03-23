<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { fetchControlConfig, saveControlConfig } from "./api/controlConfig";
import { fetchDashboardSnapshot } from "./api/dashboard";
import IconGlyph from "./components/IconGlyph.vue";
import SectionCard from "./components/SectionCard.vue";
import StatusPill from "./components/StatusPill.vue";
import {
  type ControlChannelConfig,
  type ControlConfig,
  type DashboardSnapshot,
  navStructure,
  pageMeta,
  type PageId,
} from "./data/dashboard";

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
let refreshTimer: number | undefined;

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
const gatewayControl = computed(() => controlDraft.value?.gateway ?? null);
const controlChannels = computed(() => controlDraft.value?.channels ?? []);
const controlBindings = computed(() => controlDraft.value?.bindings ?? []);
const controlAgents = computed(() => controlDraft.value?.agents ?? []);
const generatedAtLabel = computed(() => {
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

async function loadSnapshot() {
  loading.value = true;
  error.value = "";

  try {
    snapshot.value = await fetchDashboardSnapshot();
  } catch (err) {
    const message = err instanceof Error ? err.message : "failed to load dashboard";
    error.value = message;
  } finally {
    loading.value = false;
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
  await loadSnapshot();
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
  void Promise.all([loadSnapshot(), loadControlState()]);
  refreshTimer = window.setInterval(() => {
    void loadSnapshot();
  }, 15000);
});

onBeforeUnmount(() => {
  window.clearInterval(refreshTimer);
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
      <div class="main-body">
        <header class="page-header">
          <div>
            <h1>{{ currentPage.title }}</h1>
            <p>{{ currentPage.subtitle }}</p>
            <small v-if="generatedAtLabel" class="page-meta">Last updated {{ generatedAtLabel }}</small>
          </div>

          <div class="page-actions" v-if="currentPage.actions?.length">
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

        <template v-else-if="activePage === 'overview'">
          <div class="page-stack">
            <section class="metric-grid">
              <article v-for="item in overviewCards" :key="item.label" class="metric-card">
                <div class="metric-top">
                  <span>{{ item.label }}</span>
                  <StatusPill :label="item.value" :tone="item.tone" />
                </div>
                <p>{{ item.note }}</p>
              </article>
            </section>

            <section class="overview-panel-grid">
              <SectionCard
                v-for="panel in overviewPanels"
                :key="panel.title"
                :eyebrow="panel.note"
                :title="panel.title"
              >
                <ul class="bullet-list">
                  <li v-for="entry in panel.items" :key="entry">{{ entry }}</li>
                </ul>
              </SectionCard>
            </section>

            <SectionCard
              eyebrow="Control"
              title="Gateway Access"
              note="保存后会同步到配置文件并立即应用到运行态"
            >
              <div class="section-stack">
                <div v-if="controlLoading" class="runtime-banner tone-sky">
                  <p>Loading control config...</p>
                </div>

                <div v-else-if="gatewayControl" class="section-stack">
                  <div v-if="controlIssues.length" class="runtime-banner tone-rose">
                    <ul class="feedback-list">
                      <li v-for="issue in controlIssues" :key="issue">{{ issue }}</li>
                    </ul>
                  </div>
                  <div v-else-if="controlMessage" class="runtime-banner tone-emerald">
                    <p>{{ controlMessage }}</p>
                  </div>

                  <small v-if="controlGeneratedAtLabel" class="helper-text">
                    Control config loaded at {{ controlGeneratedAtLabel }}
                  </small>

                  <div class="form-grid two-col">
                    <label class="toggle-field">
                      <span>WebSocket Auth</span>
                      <input v-model="gatewayControl.webSocketAuthEnabled" type="checkbox" />
                    </label>

                    <label class="input-field">
                      <span>WebSocket Token</span>
                      <input
                        v-model="gatewayControl.webSocketAuthToken"
                        class="text-input"
                        type="password"
                        placeholder="browser access token"
                      />
                    </label>
                  </div>

                  <div class="control-toolbar">
                    <button
                      class="secondary-button"
                      type="button"
                      :disabled="controlLoading || controlSaving"
                      @click="loadControlState"
                    >
                      Reload Config
                    </button>
                    <button
                      class="primary-button"
                      type="button"
                      :disabled="controlSaving || !isControlDirty"
                      @click="saveControlChanges"
                    >
                      {{ controlSaving ? "Saving..." : "Save and Apply" }}
                    </button>
                  </div>
                </div>
              </div>
            </SectionCard>

            <SectionCard
              eyebrow="Control"
              title="Route Bindings"
              note="修改 channel 到 agent 的运行时路由"
            >
              <div class="section-stack">
                <div v-if="controlLoading" class="runtime-banner tone-sky">
                  <p>Loading control bindings...</p>
                </div>

                <template v-else-if="controlDraft">
                  <div v-if="controlIssues.length" class="runtime-banner tone-rose">
                    <ul class="feedback-list">
                      <li v-for="issue in controlIssues" :key="issue">{{ issue }}</li>
                    </ul>
                  </div>
                  <div v-else-if="controlMessage" class="runtime-banner tone-emerald">
                    <p>{{ controlMessage }}</p>
                  </div>

                  <div class="binding-list">
                    <article
                      v-for="(binding, index) in controlBindings"
                      :key="`${binding.channel}-${binding.accountId}-${index}`"
                      class="binding-row"
                    >
                      <label class="input-field">
                        <span>Channel</span>
                        <input v-model="binding.channel" class="text-input" type="text" placeholder="telegram" />
                      </label>

                      <label class="input-field">
                        <span>Account</span>
                        <input
                          v-model="binding.accountId"
                          class="text-input"
                          type="text"
                          placeholder="default or empty"
                        />
                      </label>

                      <label class="input-field">
                        <span>Agent</span>
                        <select v-model="binding.agentId" class="select-input">
                          <option value="">Select agent</option>
                          <option v-for="agent in controlAgents" :key="agent.id" :value="agent.id">
                            {{ agent.name }}{{ agent.default ? " (default)" : "" }}
                          </option>
                        </select>
                      </label>

                      <button class="danger-button" type="button" @click="removeBindingRow(index)">
                        Remove
                      </button>
                    </article>
                  </div>

                  <div class="control-toolbar">
                    <button class="secondary-button" type="button" @click="addBindingRow">
                      Add Binding
                    </button>
                    <button
                      class="primary-button"
                      type="button"
                      :disabled="controlSaving || !isControlDirty"
                      @click="saveControlChanges"
                    >
                      {{ controlSaving ? "Saving..." : "Save and Apply" }}
                    </button>
                  </div>
                </template>
              </div>
            </SectionCard>
          </div>
        </template>

        <template v-else-if="activePage === 'channels'">
          <div class="page-stack">
            <SectionCard eyebrow="Control" title="Channels">
              <div class="table-wrap">
                <div class="table-head six">
                  <span>Name</span>
                  <span>Account</span>
                  <span>Route</span>
                  <span>Mode</span>
                  <span>Status</span>
                  <span>Health</span>
                </div>
                <div v-for="item in channels" :key="`${item.name}-${item.account}`" class="table-row six">
                  <strong>{{ item.name }}</strong>
                  <span>{{ item.account }}</span>
                  <span>{{ item.route }}</span>
                  <span>{{ item.mode }}</span>
                  <StatusPill :label="item.status" :tone="item.tone" />
                  <span>{{ item.health }}</span>
                </div>
              </div>
            </SectionCard>

            <SectionCard
              eyebrow="Control"
              title="Channel Config Editor"
              note="修改账号配置后会写回 config.yaml 并热加载 channel manager"
            >
              <div class="section-stack">
                <div v-if="controlLoading" class="runtime-banner tone-sky">
                  <p>Loading channel control config...</p>
                </div>

                <template v-else-if="controlDraft">
                  <div v-if="controlIssues.length" class="runtime-banner tone-rose">
                    <ul class="feedback-list">
                      <li v-for="issue in controlIssues" :key="issue">{{ issue }}</li>
                    </ul>
                  </div>
                  <div v-else-if="controlMessage" class="runtime-banner tone-emerald">
                    <p>{{ controlMessage }}</p>
                  </div>

                  <div v-if="!controlChannels.length" class="runtime-banner tone-slate">
                    <p>No channel entries were detected in the current config file.</p>
                  </div>

                  <div v-else class="channel-editor-list">
                    <article v-for="item in controlChannels" :key="`${item.channel}-${item.accountId}`" class="channel-editor-card">
                      <div class="info-head">
                        <div>
                          <strong>{{ channelLabel(item) }}</strong>
                          <p class="muted">{{ channelHint(item) }}</p>
                        </div>
                        <label class="toggle-inline">
                          <input v-model="item.enabled" type="checkbox" />
                          <span>Enabled</span>
                        </label>
                      </div>

                      <div class="field-summary">
                        <p>Required when enabled: {{ channelRequiredSummary(item) }}</p>
                        <p v-if="item.enabled && missingRequiredFieldSummary(item)" class="field-warning">
                          Missing: {{ missingRequiredFieldSummary(item) }}
                        </p>
                      </div>

                      <div class="form-grid three-col">
                        <label class="input-field">
                          <span>Name</span>
                          <input v-model="item.name" class="text-input" type="text" placeholder="display name" />
                        </label>

                        <label v-if="supportsField(item.channel, 'mode')" class="input-field">
                          <span>Mode</span>
                          <select v-if="item.channel === 'wework'" v-model="item.mode" class="select-input">
                            <option value="webhook">webhook</option>
                            <option value="websocket">websocket</option>
                          </select>
                          <select v-else-if="item.channel === 'weixin'" v-model="item.mode" class="select-input">
                            <option value="bridge">bridge</option>
                            <option value="direct">direct</option>
                          </select>
                          <input
                            v-else
                            v-model="item.mode"
                            class="text-input"
                            type="text"
                            placeholder="webhook / websocket"
                          />
                        </label>

                        <label v-if="supportsField(item.channel, 'token')" class="input-field">
                          <span>Token</span>
                          <input v-model="item.token" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'baseUrl')" class="input-field full-width">
                          <span>Base URL</span>
                          <input v-model="item.baseUrl" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'cdnBaseUrl')" class="input-field full-width">
                          <span>CDN Base URL</span>
                          <input v-model="item.cdnBaseUrl" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'proxy')" class="input-field full-width">
                          <span>Proxy</span>
                          <input v-model="item.proxy" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'appId')" class="input-field">
                          <span>App ID</span>
                          <input v-model="item.appId" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'appSecret')" class="input-field">
                          <span>App Secret</span>
                          <input v-model="item.appSecret" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'corpId')" class="input-field">
                          <span>Corp ID</span>
                          <input v-model="item.corpId" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'agentId')" class="input-field">
                          <span>Agent ID</span>
                          <input v-model="item.agentId" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'secret')" class="input-field">
                          <span>Secret</span>
                          <input v-model="item.secret" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'botId')" class="input-field">
                          <span>Bot ID</span>
                          <input v-model="item.botId" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'botSecret')" class="input-field">
                          <span>Bot Secret</span>
                          <input v-model="item.botSecret" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'clientId')" class="input-field">
                          <span>Client ID</span>
                          <input v-model="item.clientId" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'clientSecret')" class="input-field">
                          <span>Client Secret</span>
                          <input v-model="item.clientSecret" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'bridgeUrl')" class="input-field full-width">
                          <span>Bridge URL</span>
                          <input v-model="item.bridgeUrl" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'webhookUrl')" class="input-field full-width">
                          <span>Webhook URL</span>
                          <input v-model="item.webhookUrl" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'webSocketUrl')" class="input-field full-width">
                          <span>WebSocket URL</span>
                          <input v-model="item.webSocketUrl" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'aesKey')" class="input-field">
                          <span>AES Key</span>
                          <input v-model="item.aesKey" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'encodingAESKey')" class="input-field">
                          <span>Encoding AES Key</span>
                          <input v-model="item.encodingAESKey" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'encryptKey')" class="input-field">
                          <span>Encrypt Key</span>
                          <input v-model="item.encryptKey" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'verificationToken')" class="input-field">
                          <span>Verification Token</span>
                          <input v-model="item.verificationToken" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'serverUrl')" class="input-field full-width">
                          <span>Server URL</span>
                          <input v-model="item.serverUrl" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'appToken')" class="input-field">
                          <span>App Token</span>
                          <input v-model="item.appToken" class="text-input" type="text" />
                        </label>

                        <label v-if="supportsField(item.channel, 'priority')" class="input-field">
                          <span>Priority</span>
                          <input v-model.number="item.priority" class="text-input" type="number" min="0" />
                        </label>

                        <label v-if="supportsField(item.channel, 'webhookPort')" class="input-field">
                          <span>Webhook Port</span>
                          <input v-model.number="item.webhookPort" class="text-input" type="number" min="0" />
                        </label>

                        <label class="input-field full-width">
                          <span>Allowed IDs</span>
                          <input
                            :value="allowedIdsText(item)"
                            class="text-input"
                            type="text"
                            placeholder="comma-separated"
                            @input="onAllowedIdsInput(item, $event)"
                          />
                        </label>
                      </div>
                    </article>
                  </div>

                  <div class="control-toolbar">
                    <button
                      class="secondary-button"
                      type="button"
                      :disabled="controlLoading || controlSaving"
                      @click="loadControlState"
                    >
                      Reload Config
                    </button>
                    <button
                      class="primary-button"
                      type="button"
                      :disabled="controlSaving || !isControlDirty"
                      @click="saveControlChanges"
                    >
                      {{ controlSaving ? "Saving..." : "Save and Apply" }}
                    </button>
                  </div>
                </template>
              </div>
            </SectionCard>
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
