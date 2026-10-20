// The wait before a dropped scanner stream is opened again, shared by the desktop and the phone.
// Each wait is spread by up to a fifth either way, so the clients a server restart dropped together
// do not all come back on the same beat.

export const RECONNECT_DELAYS_MS = [1_000, 2_000, 5_000, 10_000, 30_000] as const;
/** How far, as a share of the step, one wait may move either way. */
export const RECONNECT_JITTER = 0.2;

/**
 * The wait before reconnect number `attempt` (0 for the first): 1, 2, 5, 10, then 30 s, each moved
 * by `random()` (a number in [0, 1), 0.5 keeping the step exact) within ±RECONNECT_JITTER.
 */
export function reconnectDelay(attempt: number, random: () => number = Math.random): number {
  const steps: readonly number[] = RECONNECT_DELAYS_MS;
  const step = steps[Math.min(Math.max(attempt, 0), steps.length - 1)] ?? 0;
  const spread = Math.min(Math.max(random(), 0), 1) * 2 - 1;
  return Math.round(step * (1 + RECONNECT_JITTER * spread));
}
