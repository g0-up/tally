// Query-key factories for the platform queries the shell owns. Feature keys follow the same shape:
// [domain, ...scope]. Invalidating a prefix (e.g. platformKeys.all) refreshes everything below it.

export const platformKeys = {
  all: ["platform"] as const,
  me: () => ["platform", "me"] as const,
  tenant: () => ["platform", "tenant"] as const,
  onboarding: () => ["platform", "onboarding"] as const,
  subscription: () => ["platform", "subscription"] as const,
  entitlements: () => ["platform", "entitlements"] as const,
  unreadCount: () => ["platform", "unread-count"] as const,
};

export const adminKeys = {
  all: ["admin"] as const,
  me: () => ["admin", "me"] as const,
};
