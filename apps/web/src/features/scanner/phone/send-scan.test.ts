import { HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { createCsrfSource } from "../../../shared/api/csrf";
import { createHttpClient } from "../../../shared/api/http";
import { API_BASE } from "../../../shared/api/endpoints";
import { phoneScanServer } from "../../../test/msw/scanner";
import { outcomeCopy } from "./outcome-copy";
import { SEND_RETRY_DELAYS_MS, newScan, sendScan } from "./send-scan";

function setup() {
  const api = phoneScanServer({ state: "claimed" });
  const client = createHttpClient({ baseUrl: API_BASE, csrf: createCsrfSource([]) });
  const waits: number[] = [];
  const sleep = (ms: number) => {
    waits.push(ms);
    return Promise.resolve();
  };
  return { api, client, waits, sleep };
}

describe("sendScan", () => {
  it("sends the code with a fresh Idempotency-Key per detection", async () => {
    const { api, client, sleep } = setup();
    const first = newScan("8934563138165");
    const second = newScan("8934563138165");
    expect(await sendScan(client, first, { sleep })).toEqual({ outcome: "added", product_name: "Mứt gừng", unit_name: "Hộp" });
    await sendScan(client, second, { sleep });
    expect(api.scans).toEqual([
      { code: "8934563138165", key: first.key },
      { code: "8934563138165", key: second.key },
    ]);
    expect(first.key).not.toBe(second.key);
  });

  it("retries an unanswered scan under the same key after 0.5, 1 and 2 s", async () => {
    const { api, client, waits, sleep } = setup();
    let failures = 3;
    api.onScan(() => (failures-- > 0 ? HttpResponse.error() : undefined));
    const scan = newScan("8934563138165");
    expect((await sendScan(client, scan, { sleep })).outcome).toBe("added");
    expect(waits).toEqual([...SEND_RETRY_DELAYS_MS]);
    expect(api.scans.map((s) => s.key)).toEqual([scan.key, scan.key, scan.key, scan.key]);
  });

  it("retries a server error the same way", async () => {
    const { api, client, waits, sleep } = setup();
    let failures = 1;
    api.onScan(() => (failures-- > 0 ? new HttpResponse("<html>Bad gateway</html>", { status: 502 }) : undefined));
    expect((await sendScan(client, newScan("123"), { sleep })).outcome).toBe("added");
    expect(waits).toEqual([500]);
  });

  it("gives up after the fourth unanswered attempt", async () => {
    const { api, client, sleep } = setup();
    api.onScan(() => HttpResponse.error());
    await expect(sendScan(client, newScan("123"), { sleep })).rejects.toMatchObject({ status: 0 });
    expect(api.scans).toHaveLength(4);
  });

  it("never retries a timeout: the desktop may have applied the code", async () => {
    const { api, client, waits, sleep } = setup();
    api.answers.set("123", { outcome: "timeout" });
    const result = await sendScan(client, newScan("123"), { sleep });
    expect(api.scans).toHaveLength(1);
    expect(waits).toEqual([]);
    expect(outcomeCopy(result).text).toBe("Máy tính chưa phản hồi. Kiểm tra màn hình máy tính trước khi quét lại.");
  });

  it("throws refusals at once", async () => {
    const { api, client, sleep } = setup();
    api.session.state = "pending";
    await expect(sendScan(client, newScan("123"), { sleep })).rejects.toMatchObject({ status: 409, code: "scan_pending_approval" });
    expect(api.scans).toHaveLength(1);
  });
});
