export type Tone = "amber" | "emerald" | "rose" | "sky" | "slate";

export type PageId =
  | "chat"
  | "overview"
  | "channels"
  | "instances"
  | "sessions"
  | "cron"
  | "skills"
  | "nodes"
  | "config"
  | "debug"
  | "logs"
  | "docs";

export type IconName =
  | "menu"
  | "message-square"
  | "bar-chart-2"
  | "link-2"
  | "radio"
  | "file-text"
  | "settings"
  | "zap"
  | "monitor"
  | "bug"
  | "book-open"
  | "bot";

export interface NavItem {
  id: PageId;
  label: string;
  icon: IconName;
}

export interface NavGroup {
  category: string;
  items: NavItem[];
}

export interface PageMeta {
  title: string;
  subtitle: string;
  actions?: string[];
}

export interface DashboardAlert {
  text: string;
  tone: Tone;
}

export interface DashboardQuickAction {
  label: string;
  note: string;
}

export interface DashboardCard {
  label: string;
  value: string;
  note: string;
  tone: Tone;
}

export interface DashboardOverviewPanel {
  title: string;
  note: string;
  items: string[];
}

export interface DashboardChannel {
  name: string;
  account: string;
  route: string;
  mode: string;
  status: string;
  health: string;
  tone: Tone;
}

export interface DashboardInstance {
  name: string;
  kind: string;
  endpoint: string;
  auth: string;
  status: string;
  lastSeen: string;
  tone: Tone;
}

export interface DashboardSession {
  key: string;
  agent: string;
  channel: string;
  messages: string;
  updatedAt: string;
  state: string;
  tone: Tone;
}

export interface DashboardCronJob {
  name: string;
  schedule: string;
  target: string;
  delivery: string;
  nextRun: string;
  state: string;
  tone: Tone;
}

export interface DashboardSkill {
  name: string;
  source: string;
  requires: string;
  scope: string;
  state: string;
  tone: Tone;
}

export interface DashboardNode {
  name: string;
  role: string;
  provider: string;
  workspace: string;
  state: string;
  tools: string[];
  tone: Tone;
}

export interface DashboardConfigField {
  label: string;
  value: string;
}

export interface DashboardConfigGroup {
  title: string;
  fields: DashboardConfigField[];
}

export interface DashboardConfigView {
  groups: DashboardConfigGroup[];
  preview: string;
}

export interface DashboardDebugItem {
  title: string;
  description: string;
  state: string;
  tone: Tone;
}

export interface DashboardLogItem {
  time: string;
  level: string;
  origin: string;
  message: string;
  tone: Tone;
}

export interface DashboardDocItem {
  title: string;
  path: string;
  description: string;
}

export interface DashboardSnapshot {
  generatedAt: string;
  chat: {
    alerts: DashboardAlert[];
    quickActions: DashboardQuickAction[];
  };
  overview: {
    cards: DashboardCard[];
    panels: DashboardOverviewPanel[];
  };
  channels: DashboardChannel[];
  instances: DashboardInstance[];
  sessions: DashboardSession[];
  cronJobs: DashboardCronJob[];
  skills: DashboardSkill[];
  nodes: DashboardNode[];
  config: DashboardConfigView;
  debug: DashboardDebugItem[];
  logs: DashboardLogItem[];
  docs: DashboardDocItem[];
}

export interface ControlGatewayConfig {
  webSocketAuthEnabled: boolean;
  webSocketAuthToken: string;
}

export interface ControlChannelConfig {
  channel: string;
  accountId: string;
  legacy: boolean;
  enabled: boolean;
  name: string;
  mode: string;
  token: string;
  appId: string;
  appSecret: string;
  corpId: string;
  agentId: string;
  secret: string;
  botId: string;
  botSecret: string;
  clientId: string;
  clientSecret: string;
  bridgeUrl: string;
  webhookUrl: string;
  webSocketUrl: string;
  aesKey: string;
  encodingAESKey: string;
  encryptKey: string;
  verificationToken: string;
  webhookPort: number;
  serverUrl: string;
  appToken: string;
  priority: number;
  allowedIds: string[];
}

export interface ControlBindingConfig {
  channel: string;
  accountId: string;
  agentId: string;
}

export interface ControlAgentOption {
  id: string;
  name: string;
  default: boolean;
}

export interface ControlConfig {
  generatedAt: string;
  gateway: ControlGatewayConfig;
  channels: ControlChannelConfig[];
  bindings: ControlBindingConfig[];
  agents: ControlAgentOption[];
}

export interface ControlConfigSaveResult {
  saved: boolean;
  applied: boolean;
  configPath: string;
  generatedAt: string;
  message: string;
}

export const navStructure: NavGroup[] = [
  {
    category: "Chat",
    items: [{ id: "chat", label: "Chat", icon: "message-square" }],
  },
  {
    category: "Control",
    items: [
      { id: "overview", label: "Overview", icon: "bar-chart-2" },
      { id: "channels", label: "Channels", icon: "link-2" },
      { id: "instances", label: "Instances", icon: "radio" },
      { id: "sessions", label: "Sessions", icon: "file-text" },
      { id: "cron", label: "Cron Jobs", icon: "settings" },
    ],
  },
  {
    category: "Agent",
    items: [
      { id: "skills", label: "Skills", icon: "zap" },
      { id: "nodes", label: "Nodes", icon: "monitor" },
    ],
  },
  {
    category: "Settings",
    items: [
      { id: "config", label: "Config", icon: "settings" },
      { id: "debug", label: "Debug", icon: "bug" },
      { id: "logs", label: "Logs", icon: "file-text" },
    ],
  },
  {
    category: "Resources",
    items: [{ id: "docs", label: "Docs", icon: "book-open" }],
  },
];

export const pageMeta: Record<PageId, PageMeta> = {
  chat: {
    title: "Chat",
    subtitle: "Direct gateway chat session for quick interventions.",
    actions: ["Refresh", "Health Check"],
  },
  overview: {
    title: "Overview",
    subtitle: "Operational summary for the current SunClaw control plane.",
    actions: ["Refresh", "Run Health Check"],
  },
  channels: {
    title: "Channels",
    subtitle: "Manage inbound and outbound channel connectors, routing and health.",
    actions: ["Refresh", "Inspect Bindings"],
  },
  instances: {
    title: "Instances",
    subtitle: "Track gateway surfaces, trusted clients and active endpoints.",
    actions: ["Refresh", "Copy WS URL"],
  },
  sessions: {
    title: "Sessions",
    subtitle: "Inspect active sessions, subagents and archival windows.",
    actions: ["Refresh", "Search Session"],
  },
  cron: {
    title: "Cron Jobs",
    subtitle: "Configure scheduled automation and delivery targets.",
    actions: ["Refresh", "Open Store"],
  },
  skills: {
    title: "Skills",
    subtitle: "Browse installed skills, requirements and usage scope.",
    actions: ["Refresh", "Validate Skills"],
  },
  nodes: {
    title: "Nodes",
    subtitle: "Monitor agent nodes, providers, tools and working directories.",
    actions: ["Refresh", "Sync Providers"],
  },
  config: {
    title: "Config",
    subtitle: "Review grouped configuration before editing or applying changes.",
    actions: ["Refresh", "Preview Diff"],
  },
  debug: {
    title: "Debug",
    subtitle: "Inspect RPC surfaces, approvals and local runtime diagnostics.",
    actions: ["Refresh", "Open RPC"],
  },
  logs: {
    title: "Logs",
    subtitle: "Recent runtime events, warnings and execution traces.",
    actions: ["Refresh", "Tail Errors"],
  },
  docs: {
    title: "Docs",
    subtitle: "Shortcuts to architecture, config and operating guides.",
    actions: ["Refresh", "Open README"],
  },
};
