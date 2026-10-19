import { describe, expect, it } from "vitest";
import { createLimiter } from "./concurrency";

const deferred = () => {
  let resolve: () => void = () => undefined;
  let reject: (e: unknown) => void = () => undefined;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

describe("createLimiter", () => {
  it("runs at most the limit at once and starts the waiting tasks in order", async () => {
    const limit = createLimiter(2);
    const gates = [deferred(), deferred(), deferred(), deferred()];
    const started: number[] = [];
    const runs = gates.map((g, i) =>
      limit(async () => {
        started.push(i);
        await g.promise;
        return i;
      }),
    );
    await Promise.resolve();
    expect(started).toEqual([0, 1]);

    gates[1]?.resolve();
    await expect(runs[1]).resolves.toBe(1);
    await Promise.resolve();
    expect(started).toEqual([0, 1, 2]);

    gates[0]?.reject(new Error("refused"));
    await expect(runs[0]).rejects.toThrow("refused");
    await Promise.resolve();
    expect(started).toEqual([0, 1, 2, 3]);

    gates[2]?.resolve();
    gates[3]?.resolve();
    await expect(Promise.all(runs.slice(2))).resolves.toEqual([2, 3]);
  });

  it("refuses a limit that is not a positive integer", () => {
    expect(() => createLimiter(0)).toThrow("positive integer");
    expect(() => createLimiter(1.5)).toThrow("positive integer");
  });
});
