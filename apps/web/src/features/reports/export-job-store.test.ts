import { afterEach, describe, expect, it, vi } from "vitest";

// Each test loads the store afresh, as a reloaded tab would, over what the tab's storage holds.

const STORAGE_KEY = "tally.report-exports";

const freshStore = async () => {
  vi.resetModules();
  return import("./export-job-store");
};

afterEach(() => {
  window.sessionStorage.clear();
  vi.restoreAllMocks();
});

describe("export job store", () => {
  it("keeps only the export id across a reload, per report and ledger", async () => {
    const before = await freshStore();
    before.setStoredExport("nxt", { id: "exp-1", round: 3 });
    before.setStoredExport("debt_aging:payable", { id: "exp-2", round: 0 });
    expect(JSON.parse(window.sessionStorage.getItem(STORAGE_KEY) ?? "null")).toEqual({ nxt: "exp-1", "debt_aging:payable": "exp-2" });

    const after = await freshStore();
    expect(after.storedExport("nxt")).toEqual({ id: "exp-1", round: 0 });
    expect(after.storedExport("debt_aging:payable")).toEqual({ id: "exp-2", round: 0 });
    expect(after.storedExport("debt_aging:receivable")).toBeNull();
  });

  it("forgets an export once cleared", async () => {
    const before = await freshStore();
    before.setStoredExport("nxt", { id: "exp-1", round: 0 });
    before.clearStoredExport("nxt");
    expect(before.storedExport("nxt")).toBeNull();
    expect((await freshStore()).storedExport("nxt")).toBeNull();
  });

  it.each([
    ["text that is not JSON", "{not json"],
    ["a list", JSON.stringify(["exp-1"])],
    ["ids of the wrong type", JSON.stringify({ nxt: 42, revenue: "", kits: { id: "exp-1" } })],
  ])("drops %s found in storage", async (_, raw) => {
    window.sessionStorage.setItem(STORAGE_KEY, raw);
    const store = await freshStore();
    expect(store.storedExport("nxt")).toBeNull();
    expect(store.storedExport("revenue")).toBeNull();
    expect(store.storedExport("kits")).toBeNull();
  });

  it("knows nothing of a report's export only until one is saved, cleared or picked up", async () => {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ nxt: "exp-1" }));
    const store = await freshStore();
    expect(store.unknownExport("nxt")).toBe(false);
    expect(store.unknownExport("revenue")).toBe(true);
    expect(store.unknownExport("kits")).toBe(true);

    store.setStoredExport("revenue", { id: "exp-2", round: 0 });
    store.setStoredExport("kits", { id: "exp-3", round: 0 });
    store.clearStoredExport("kits");
    expect(store.unknownExport("revenue")).toBe(false);
    expect(store.unknownExport("kits")).toBe(false);

    store.resetStoredExports();
    expect(store.unknownExport("kits")).toBe(true);
  });

  it("still follows an export in memory when the storage refuses access", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    const store = await freshStore();
    expect(store.storedExport("nxt")).toBeNull();
    store.setStoredExport("nxt", { id: "exp-1", round: 0 });
    expect(store.storedExport("nxt")).toEqual({ id: "exp-1", round: 0 });
  });
});
