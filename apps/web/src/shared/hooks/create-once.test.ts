import { describe, expect, it, vi } from "vitest";
import { ApiError, networkError } from "../api/api-error";
import { OUTCOME_UNKNOWN_MESSAGE, OutcomeUnknownError, createOnce, newCreateState, outcomeUnknown, requestJson, unsettled } from "./create-once";

interface Body {
  name: string;
}
interface Doc {
  id: string;
  name: string;
  version: number;
}

const refusal = () => new ApiError({ status: 422, code: "validation_failed", message: "Sai." });
const crash = () => new ApiError({ status: 503, code: "unavailable", message: "Lỗi." });
const inProgress = () => new ApiError({ status: 409, code: "idempotency_key_in_progress", message: "?" });
const reauth = () => new ApiError({ status: 403, code: "reauth_required", message: "?" });
const expired = () => new ApiError({ status: 401, code: "unauthenticated", message: "?" });

/** A create endpoint that fails with `failures` first, then stores the body it gets as version 1. */
function endpoint(...failures: unknown[]) {
  const send = vi.fn<(b: Body) => Promise<Doc>>();
  for (const f of failures) send.mockRejectedValueOnce(f);
  send.mockImplementation(async (b) => ({ id: "d1", name: b.name, version: 1 }));
  const update = vi.fn(async (d: Doc, b: Body): Promise<Doc> => ({ ...d, name: b.name, version: d.version + 1 }));
  return { send, update, names: () => send.mock.calls.map(([b]) => b.name) };
}

describe("createOnce", () => {
  it("sends the body and keeps the answer, so a later attempt does not create again", async () => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint();
    expect((await createOnce(state, { name: "A" }, api)).result).toEqual({ id: "d1", name: "A", version: 1 });
    expect((await createOnce(state, { name: "A" }, api)).result.version).toBe(1);
    expect(api.send).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["no answer", networkError()],
    ["a server error", crash()],
  ])("resends the very same body after %s", async (_what, failure) => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint(failure);
    const lost = await createOnce(state, { name: "A" }, api).catch((e: unknown) => e);
    // Not "Chưa ghi sổ.": the document may be stored.
    expect(lost).toBeInstanceOf(OutcomeUnknownError);
    expect((lost as OutcomeUnknownError).notice).toBe(OUTCOME_UNKNOWN_MESSAGE);
    expect((lost as OutcomeUnknownError).message).toBe(OUTCOME_UNKNOWN_MESSAGE);
    expect((lost as OutcomeUnknownError).cause).toBe(failure);
    expect(state.sent).toEqual({ name: "A" });
    expect(unsettled(state)).toBe(true);
    await createOnce(state, { name: "A" }, api);
    expect(api.names()).toEqual(["A", "A"]);
    expect(unsettled(state)).toBe(false);
  });

  it("passes a refusal on as it is", async () => {
    const failure = refusal();
    const state = newCreateState<Body, Doc>();
    await expect(createOnce(state, { name: "A" }, endpoint(failure))).rejects.toBe(failure);
    expect(unsettled(state)).toBe(false);
  });

  it("keeps the body through a password prompt, so the retry resends it", async () => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint(networkError(), reauth());
    await expect(createOnce(state, { name: "A" }, api)).rejects.toThrow();
    await expect(createOnce(state, { name: "B" }, api)).rejects.toThrow();
    expect(state.sent).toEqual({ name: "A" });
    await createOnce(state, { name: "B" }, api);
    expect(api.names()).toEqual(["A", "A", "A"]);
  });

  it("drops the body when the session ended, so the next session sends its own form", async () => {
    const state = newCreateState<Body, Doc>();
    // The kept body meets the expired session, and so does the form's after it.
    const api = endpoint(networkError(), expired(), expired());
    await expect(createOnce(state, { name: "A" }, api)).rejects.toThrow();
    await expect(createOnce(state, { name: "B" }, api)).rejects.toThrow();
    expect(state.sent).toBeNull();
    expect(await createOnce(state, { name: "B" }, api)).toEqual({ result: { id: "d1", name: "B", version: 1 }, edited: false });
    expect(api.names()).toEqual(["A", "A", "B", "B"]);
  });

  it("writes edits made since the lost answer onto the replayed document, from its version", async () => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint(networkError());
    await expect(createOnce(state, { name: "A" }, api)).rejects.toThrow();
    const made = await createOnce(state, { name: "B" }, api);
    expect(api.names()).toEqual(["A", "A"]);
    expect(api.update).toHaveBeenCalledWith({ id: "d1", name: "A", version: 1 }, { name: "B" });
    expect(made).toEqual({ result: { id: "d1", name: "B", version: 2 }, edited: false });
    expect(state.sent).toEqual({ name: "B" });
  });

  it("does not update when the form still holds the body that was created", async () => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint(networkError());
    await expect(createOnce(state, { name: "A" }, api)).rejects.toThrow();
    expect(await createOnce(state, { name: "A" }, api)).toEqual({ result: { id: "d1", name: "A", version: 1 }, edited: false });
    expect(api.update).not.toHaveBeenCalled();
  });

  it("reports the edits as unsaved when there is no update", async () => {
    const state = newCreateState<Body, Doc>();
    const { send } = endpoint(networkError());
    await expect(createOnce(state, { name: "A" }, { send })).rejects.toThrow();
    expect(await createOnce(state, { name: "B" }, { send })).toEqual({ result: { id: "d1", name: "A", version: 1 }, edited: true });
  });

  it("updates again after a failed update instead of creating again", async () => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint(networkError());
    api.update.mockRejectedValueOnce(refusal());
    await expect(createOnce(state, { name: "A" }, api)).rejects.toThrow();
    await expect(createOnce(state, { name: "B" }, api)).rejects.toThrow();
    expect((await createOnce(state, { name: "B" }, api)).result.name).toBe("B");
    expect(api.names()).toEqual(["A", "A"]);
    expect(api.update).toHaveBeenCalledTimes(2);
  });

  it("drops a refused body, so the next attempt sends the form as it is", async () => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint(refusal());
    await expect(createOnce(state, { name: "A" }, api)).rejects.toThrow();
    expect(state.sent).toBeNull();
    expect(await createOnce(state, { name: "B" }, api)).toEqual({ result: { id: "d1", name: "B", version: 1 }, edited: false });
    expect(api.names()).toEqual(["A", "B"]);
    expect(api.update).not.toHaveBeenCalled();
  });

  it("keeps the body while the server is still working on the key", async () => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint(inProgress());
    await expect(createOnce(state, { name: "A" }, api)).rejects.toThrow();
    expect(state.sent).toEqual({ name: "A" });
    await createOnce(state, { name: "B" }, api);
    expect(api.names()).toEqual(["A", "A"]);
  });

  it("sends the edited body when the server refused the unanswered one", async () => {
    const state = newCreateState<Body, Doc>();
    const api = endpoint(crash(), refusal());
    await expect(createOnce(state, { name: "A" }, api)).rejects.toThrow();
    expect((await createOnce(state, { name: "B" }, api)).result.name).toBe("B");
    expect(api.names()).toEqual(["A", "A", "B"]);
  });
});

describe("requestJson", () => {
  it("writes the same amount one way, whatever trailing zeros it carries", () => {
    expect(requestJson({ price: "18000.00", qty: "1.50" })).toBe(requestJson({ qty: "1.5", price: "18000" }));
    expect(requestJson({ lines: [{ price: "-2.0" }] })).toBe(requestJson({ lines: [{ price: "-2" }] }));
  });

  it("keeps strings that are not fractional amounts as they are", () => {
    expect(requestJson({ code: "001" })).not.toBe(requestJson({ code: "1" }));
    expect(requestJson({ price: "18000" })).not.toBe(requestJson({ price: "18001" }));
  });
});

describe("createOnce with amounts", () => {
  it("treats an amount written with trailing zeros as the body it kept, and resends that body as it was", async () => {
    const state = newCreateState<{ price: string }, { id: string }>();
    const send = vi.fn<(b: { price: string }) => Promise<{ id: string }>>().mockRejectedValueOnce(networkError()).mockResolvedValue({ id: "d1" });
    const update = vi.fn(async (d: { id: string }) => d);
    await expect(createOnce(state, { price: "18000.00" }, { send, update })).rejects.toThrow();
    expect(await createOnce(state, { price: "18000" }, { send, update })).toEqual({ result: { id: "d1" }, edited: false });
    expect(send.mock.calls.map(([b]) => b.price)).toEqual(["18000.00", "18000.00"]);
    expect(update).not.toHaveBeenCalled();
  });
});

describe("outcomeUnknown", () => {
  it("holds for answers that do not say whether anything was stored", () => {
    expect(outcomeUnknown(networkError())).toBe(true);
    expect(outcomeUnknown(crash())).toBe(true);
    expect(outcomeUnknown(new ApiError({ status: 201, code: "bad_response", message: "?" }))).toBe(true);
    expect(outcomeUnknown(inProgress())).toBe(true);
    expect(outcomeUnknown(refusal())).toBe(false);
    expect(outcomeUnknown(new ApiError({ status: 409, code: "idempotency_key_reused", message: "?" }))).toBe(false);
    expect(outcomeUnknown(new Error("boom"))).toBe(false);
  });
});
