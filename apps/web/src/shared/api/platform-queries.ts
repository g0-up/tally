import { useCallback, useEffect, useState } from "react";
import { queryOptions, useQuery } from "@tanstack/react-query";
import { BUSINESS_TZ, isTimeZone, msUntilNextBusinessDay, todayBusinessDate } from "../lib/dates";
import { isSessionExpired } from "./api-error";
import { useApiClient } from "./api-context";
import {
  PATHS,
  adminMeSchema,
  entitlementsSchema,
  meSchema,
  onboardingSchema,
  subscriptionViewSchema,
  tenantSchema,
  unreadCountSchema,
} from "./endpoints";
import type { HttpClient } from "./http";
import { adminKeys, platformKeys } from "./query-keys";

// Queries of the platform endpoints the shell, guards and every feature read: session, tenant,
// subscription, entitlements, onboarding and the notification count. All are silent: the guards
// and the shell decide what a failure means, not the global error handler.

const SESSION_STALE_MS = 60_000;

/** Session probes are not retried on 401; anything else gets one retry. */
function retryUnlessSignedOut(failureCount: number, error: unknown): boolean {
  return !isSessionExpired(error) && failureCount < 1;
}

export function sessionQuery(client: HttpClient) {
  return queryOptions({
    queryKey: platformKeys.me(),
    queryFn: async ({ signal }) => {
      const me = await client.get(PATHS.me, { schema: meSchema, signal, silent: true });
      if (me.csrf_token) client.setCsrfToken(me.csrf_token);
      return me;
    },
    staleTime: SESSION_STALE_MS,
    retry: retryUnlessSignedOut,
  });
}

export function tenantQuery(client: HttpClient) {
  return queryOptions({
    queryKey: platformKeys.tenant(),
    queryFn: ({ signal }) => client.get(PATHS.tenant, { schema: tenantSchema, signal, silent: true }),
    staleTime: SESSION_STALE_MS,
    retry: retryUnlessSignedOut,
  });
}

export function subscriptionQuery(client: HttpClient) {
  return queryOptions({
    queryKey: platformKeys.subscription(),
    queryFn: ({ signal }) => client.get(PATHS.subscription, { schema: subscriptionViewSchema, signal, silent: true }),
    staleTime: SESSION_STALE_MS,
    retry: retryUnlessSignedOut,
  });
}

export function entitlementsQuery(client: HttpClient) {
  return queryOptions({
    queryKey: platformKeys.entitlements(),
    queryFn: ({ signal }) => client.get(PATHS.entitlements, { schema: entitlementsSchema, signal, silent: true }),
    staleTime: SESSION_STALE_MS,
    retry: retryUnlessSignedOut,
  });
}

export function onboardingQuery(client: HttpClient) {
  return queryOptions({
    queryKey: platformKeys.onboarding(),
    queryFn: ({ signal }) => client.get(PATHS.onboarding, { schema: onboardingSchema, signal, silent: true }),
    staleTime: SESSION_STALE_MS,
    retry: retryUnlessSignedOut,
  });
}

/** Unread notifications for the util bar; refreshed every minute while the tab is open. */
export function unreadCountQuery(client: HttpClient) {
  return queryOptions({
    queryKey: platformKeys.unreadCount(),
    queryFn: ({ signal }) => client.get(PATHS.unreadCount, { schema: unreadCountSchema, signal, silent: true }),
    staleTime: 30_000,
    refetchInterval: 60_000,
    retry: false,
  });
}

export function adminSessionQuery(client: HttpClient) {
  return queryOptions({
    queryKey: adminKeys.me(),
    queryFn: async ({ signal }) => {
      const me = await client.get(PATHS.adminMe, { schema: adminMeSchema, signal, silent: true });
      if (me.csrf_token) client.setCsrfToken(me.csrf_token);
      return me;
    },
    staleTime: SESSION_STALE_MS,
    retry: retryUnlessSignedOut,
  });
}

export function useSession() {
  return useQuery(sessionQuery(useApiClient()));
}

export function useTenant() {
  return useQuery(tenantQuery(useApiClient()));
}

/** The store's time zone (GET /tenant `timezone`), the business zone until it loads or when unknown. */
function useStoreTimeZone(): string {
  const timeZone = useTenant().data?.timezone;
  return timeZone && isTimeZone(timeZone) ? timeZone : BUSINESS_TZ;
}

/**
 * How far the server's clock is ahead of the device's: GET /me `server_time` against the moment the
 * answer arrived. Null until /me has loaded.
 */
function useServerClockOffset(): number | null {
  const { data, dataUpdatedAt } = useSession();
  return data ? Date.parse(data.server_time) - dataUpdatedAt : null;
}

/** The server's clock now, in ms (GET /me `server_time`, kept running as an offset); the device's until /me has loaded. */
export function useServerNow(): () => number {
  const offset = useServerClockOffset() ?? 0;
  return useCallback(() => Date.now() + offset, [offset]);
}

/**
 * Today's date at the device clock plus `offset` in the time zone. The clock is read when the hook
 * mounts and again when that day ends, so a screen left open past midnight moves to the new day
 * without a refetch.
 */
function useBusinessDay(offset: number, timeZone: string): string {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const current = new Date(Date.now() + offset);
    // An offset or zone that changed since `now` was read may already be on another day.
    const sameDay = todayBusinessDate(current, timeZone) === todayBusinessDate(new Date(now + offset), timeZone);
    const id = setTimeout(() => setNow(Date.now()), sameDay ? msUntilNextBusinessDay(current, timeZone) : 0);
    return () => clearTimeout(id);
  }, [now, offset, timeZone]);
  return todayBusinessDate(new Date(now + offset), timeZone);
}

/**
 * Today's business date by the server's clock (GET /me `server_time`, kept running as an offset) in
 * the store's time zone. Null until /me has loaded, for reads that must not ask for a date the
 * device clock made up. A document fixes its date when its intent starts; this value moves on.
 */
export function useServerBusinessDate(): string | null {
  const offset = useServerClockOffset();
  const today = useBusinessDay(offset ?? 0, useStoreTimeZone());
  return offset === null ? null : today;
}

/** Today's business date by the server's clock; the device clock only while /me has not loaded. */
export function useBusinessDate(): string {
  return useBusinessDay(useServerClockOffset() ?? 0, useStoreTimeZone());
}

export function useSubscription() {
  return useQuery(subscriptionQuery(useApiClient()));
}

export function useEntitlements() {
  return useQuery(entitlementsQuery(useApiClient()));
}

export function useOnboarding() {
  return useQuery(onboardingQuery(useApiClient()));
}

export function useUnreadCount() {
  return useQuery(unreadCountQuery(useApiClient()));
}

export function useAdminSession() {
  return useQuery(adminSessionQuery(useApiClient()));
}
