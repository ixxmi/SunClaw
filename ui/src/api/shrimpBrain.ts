import type { ShrimpBrainSnapshot } from "../data/dashboard";

export async function fetchShrimpBrainSnapshot(signal?: AbortSignal): Promise<ShrimpBrainSnapshot> {
  const response = await fetch("/api/shrimp-brain", {
    method: "GET",
    headers: {
      Accept: "application/json",
    },
    signal,
  });

  if (!response.ok) {
    throw new Error(`shrimp brain request failed: ${response.status}`);
  }

  return (await response.json()) as ShrimpBrainSnapshot;
}

export async function deleteShrimpBrainRun(runId: string): Promise<void> {
  const response = await fetch(`/api/shrimp-brain?runId=${encodeURIComponent(runId)}`, {
    method: "DELETE",
    headers: {
      Accept: "application/json",
    },
  });

  if (!response.ok) {
    throw new Error(`shrimp brain delete failed: ${response.status}`);
  }
}
