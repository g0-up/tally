// Public surface of the scanner feature: the provider the app shell mounts, the pairing card a
// screen shows next to its scan box, and the hook that sends a paired phone's codes to that screen.
export { PhonePairingCard } from "./components/phone-pairing-card";
export { CLAIM_HASH_KEY, pairingUrl } from "./pairing-url";
export { ScannerProvider } from "./scanner-provider";
export { NOT_ACCEPTING, type ScanHandler } from "./scanner-controller";
export { useScanTarget } from "./use-scan-target";
