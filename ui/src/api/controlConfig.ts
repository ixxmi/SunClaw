import type { ControlConfig, ControlConfigSaveResult } from "../data/dashboard";

export async function fetchControlConfig(signal?: AbortSignal): Promise<ControlConfig> {
  const response = await fetch("/api/control-config", {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
    signal,
  });

  if (!response.ok) {
    throw new Error(`control config request failed: ${response.status}`);
  }

  return (await response.json()) as ControlConfig;
}

export async function saveControlConfig(
  payload: ControlConfig,
  signal?: AbortSignal,
): Promise<ControlConfigSaveResult> {
  const response = await fetch("/api/control-config", {
    method: "PUT",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
    signal,
  });

  if (!response.ok) {
    const message = await response.text();
    throw new Error(message || `control config save failed: ${response.status}`);
  }

  return (await response.json()) as ControlConfigSaveResult;
}
