// Public surface of the onboarding feature. Pages are loaded by the router from ./pages; anything other
// features may import is re-exported here.
export { StepStatusChip, StepTypeTag } from "./components/step-card";
export { orderedSteps, requiredProgress, type ChecklistStep } from "./steps";
