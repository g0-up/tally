import type { Onboarding } from "../../shared/api/endpoints";

/** A step the API does not let the owner skip is still pending: the store is not ready yet. */
export function hasBlockingOnboardingStep(onboarding: Onboarding): boolean {
  return onboarding.steps.some((s) => !s.skippable && s.status === "pending");
}
