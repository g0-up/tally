import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createPlatform } from "../../app/platform";
import { QueryProvider } from "../../app/providers/query-provider";
import { TEST_ME, TEST_TENANT } from "../../test/msw/handlers";
import { msUntilNextBusinessDay } from "../lib/dates";
import { useBusinessDate, useServerBusinessDate } from "./platform-queries";
import { platformKeys } from "./query-keys";

function Today() {
  return (
    <>
      <output aria-label="business">{useBusinessDate()}</output>
      <output aria-label="server">{useServerBusinessDate() ?? "none"}</output>
    </>
  );
}

/** Renders the date hooks with /me and /tenant already answered (or not), so nothing is fetched. */
function renderToday(serverTime: string | null, timezone = TEST_TENANT.timezone) {
  const platform = createPlatform("tenant");
  if (serverTime) platform.queryClient.setQueryData(platformKeys.me(), { ...TEST_ME, server_time: serverTime });
  platform.queryClient.setQueryData(platformKeys.tenant(), { ...TEST_TENANT, timezone });
  render(
    <QueryProvider queryClient={platform.queryClient} client={platform.client}>
      <Today />
    </QueryProvider>,
  );
}

const shown = (name: string) => screen.getByRole("status", { name }).textContent;

describe("business date", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("runs on the server's clock and turns to the next day at the store's midnight", () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    // The device thinks it is mid-afternoon; the server says it is a minute to midnight.
    vi.setSystemTime(new Date("2026-10-12T08:00:00Z"));
    renderToday("2026-10-12T23:59:00+07:00");
    expect(shown("business")).toBe("2026-10-12");
    expect(shown("server")).toBe("2026-10-12");

    act(() => vi.advanceTimersByTime(59_000));
    expect(shown("business")).toBe("2026-10-12");

    act(() => vi.advanceTimersByTime(1_000));
    expect(shown("business")).toBe("2026-10-13");
    expect(shown("server")).toBe("2026-10-13");

    // And again a day later, without /me being asked again.
    act(() => vi.advanceTimersByTime(86_400_000));
    expect(shown("business")).toBe("2026-10-14");
  });

  it("turns the day at midnight in the store's own time zone", () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    vi.setSystemTime(new Date("2026-10-12T23:59:30Z"));
    renderToday("2026-10-12T23:59:30Z", "UTC");
    expect(shown("business")).toBe("2026-10-12");

    act(() => vi.advanceTimersByTime(30_000));
    expect(shown("business")).toBe("2026-10-13");
  });

  it("has no server date until /me answers, and falls back to the device clock for defaults", () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
    vi.setSystemTime(new Date("2026-10-12T16:59:59Z"));
    renderToday(null);
    expect(shown("server")).toBe("none");
    expect(shown("business")).toBe("2026-10-12");

    act(() => vi.advanceTimersByTime(1_000));
    expect(shown("business")).toBe("2026-10-13");
  });
});

describe("time to the next business day", () => {
  it("counts to midnight in the given time zone", () => {
    expect(msUntilNextBusinessDay(new Date("2026-10-12T16:59:00Z"))).toBe(60_000);
    expect(msUntilNextBusinessDay(new Date("2026-10-12T17:00:00Z"))).toBe(86_400_000);
    expect(msUntilNextBusinessDay(new Date("2026-10-12T23:59:59.500Z"), "UTC")).toBe(500);
  });
});
