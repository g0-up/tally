import { createContext, useContext } from "react";
import type { OnboardingStepKey, OpeningTotals } from "../../shared/api/endpoints";
import type { ButtonVariant } from "../../shared/ui/button";
import type { ChecklistStep, LockState } from "./steps";

/** What every step body reads from the screen around it. */
export interface OnboardingScreen extends LockState {
  steps: readonly ChecklistStep[];
  /** One gradient button per screen: "Vào cửa hàng" once the store is ready, the step's own action before. */
  cta: Extract<ButtonVariant, "primary" | "navy">;
  totals: OpeningTotals | null;
  /** When the opening balances were confirmed. */
  confirmedAt: string | null;
  goTo: (key: OnboardingStepKey) => void;
}

export const OnboardingContext = createContext<OnboardingScreen | null>(null);

export function useOnboardingScreen(): OnboardingScreen {
  const screen = useContext(OnboardingContext);
  if (!screen) throw new Error("useOnboardingScreen must be used inside the onboarding page");
  return screen;
}
