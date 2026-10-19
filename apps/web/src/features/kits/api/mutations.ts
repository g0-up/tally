import { createElement, Fragment, useRef, useState, type ReactNode } from "react";
import { z } from "zod";
import { useApiClient } from "../../../shared/api/api-context";
import type { components } from "../../../shared/api/schema";
import { useBusinessDate } from "../../../shared/api/platform-queries";
import type { Money } from "../../../shared/domain/money";
import type { Qty } from "../../../shared/domain/qty";
import { createOnce, newCreateState, useUnsettled, type CreateState } from "../../../shared/hooks/create-once";
import { PartialCommitError, runFollowUps, useCommit, type FollowUpStep } from "../../../shared/hooks/use-commit";
import { dec } from "../../../shared/lib/decimal";
import { fd } from "../../../shared/lib/format";
import { etagOf, skuKeys } from "../../catalog";
import { STOCK_POSTING_KEYS, usePerRowCommit } from "../../inventory";
import { postSalePrice, type SalePriceWrites } from "../../pricing";
import { assemblyDetailSchema, disassemblySchema, kitSchema, postingResultSchema, type AssemblyDetail, type AssemblyOrder, type Kit, type KitType } from "../schemas";
import { KIT_MESSAGES, withKitMessage } from "./adapters";
import { kitKeys } from "./queries";
import type { AssemblyRequest, BatchLineRequest, DisassemblyRequest, KitCreateRequest, RecipeRequest } from "./requests";

// Write side of the Giỏ quà screens. A kit command refreshes the kits and the SKU lists (a kit is a
// SKU); an assembly or a disassembly moves stock, so it also refreshes everything stock feeds.

const KIT_KEYS = [kitKeys.all, skuKeys.all] as const;
const POSTING_KEYS = [kitKeys.all, ...STOCK_POSTING_KEYS] as const;

/** Toast text with a name in bold: `before <b>bold</b> after`. */
const withBold = (before: string, bold: string, after: string): ReactNode => createElement(Fragment, null, before, createElement("b", null, bold), after);

/** Runs a request and maps a refusal onto the prototype sentence. */
async function kitCall<T>(run: () => Promise<T>): Promise<T> {
  try {
    return await run();
  } catch (e) {
    throw withKitMessage(e);
  }
}

const skuVersionSchema = z.object({ version: z.number().int() });

/** The SKU-level settings a kit keeps on its own SKU (prices and minimum stock). */
export interface KitSkuSettings {
  retail?: Money;
  wholesale?: Money;
  minStock?: Qty;
}

/** The settings among these that are set. */
export function skuSettings(retail: Money | null | undefined, wholesale: Money | null | undefined, minStock: Qty | null | undefined): KitSkuSettings {
  return {
    ...(retail !== null && retail !== undefined ? { retail } : {}),
    ...(wholesale !== null && wholesale !== undefined ? { wholesale } : {}),
    ...(minStock !== null && minStock !== undefined ? { minStock } : {}),
  };
}

type Client = ReturnType<typeof useApiClient>;

/**
 * A follow-up write and the value it stores. A retry skips it only while the form still asks for
 * the value it stored, so an edit made after a partial save is sent rather than reported as saved.
 */
interface ValueStep extends FollowUpStep {
  value: string;
}

/**
 * One intent's progress, so a retry continues instead of creating the kit twice. `done` maps a
 * step to the value the server holds after it (or held before the intent, for the SKU settings).
 */
interface Progress {
  key: string;
  kit: Kit | null;
  done: Map<string, string>;
  /** The kit's create request, resent unchanged while its outcome is unknown. */
  create: CreateState<KitCreateRequest, Kit>;
  writes: SalePriceWrites;
}

/** The progress once the intent's main document is stored. */
type Saved = Progress & { kit: Kit };

function useProgress() {
  const ref = useRef<Progress | null>(null);
  return {
    for: (key: string): Progress => {
      if (ref.current?.key !== key) ref.current = { key, kit: null, done: new Map(), create: newCreateState(), writes: new Map() };
      return ref.current;
    },
    clear: () => {
      ref.current = null;
    },
  };
}

const num = (v: string) => dec(v).toString();

const SETTING_STEPS = { retail: "retail", wholesale: "wholesale", minStock: "min-stock" } as const;

/** Records the SKU's current settings, so a write of the same value is skipped. */
function seed(p: Progress, current: KitSkuSettings) {
  for (const key of Object.keys(SETTING_STEPS) as (keyof KitSkuSettings)[]) {
    const value = current[key];
    if (value !== undefined) p.done.set(SETTING_STEPS[key], num(value));
  }
}

/** Runs the steps whose value the server does not hold yet and records those that went through. */
async function runSteps(steps: readonly ValueStep[], p: Progress, stored: (missing: string, reason: string) => ReactNode) {
  const done = new Set(steps.filter((s) => p.done.get(s.id) === s.value).map((s) => s.id));
  try {
    await runFollowUps(steps, done, stored, withKitMessage);
  } finally {
    for (const s of steps) if (done.has(s.id)) p.done.set(s.id, s.value);
  }
}

/**
 * Follow-up writes of the kit's SKU settings. The wholesale price never sits above the retail one
 * in between, so a retail price below the current wholesale one goes second.
 */
function skuSteps(client: Client, intentKey: string, skuId: string, s: KitSkuSettings, p: Progress): ValueStep[] {
  const price = (kind: "retail" | "wholesale", value: Money): ValueStep => ({
    id: kind,
    label: kind === "retail" ? "lưu giá bán lẻ" : "lưu giá sỉ",
    value: num(value),
    run: () => postSalePrice(client, intentKey, skuId, { kind, price: value, source: "catalog" }, p.writes),
  });
  const r = s.retail === undefined ? [] : [price("retail", s.retail)];
  const w = s.wholesale === undefined ? [] : [price("wholesale", s.wholesale)];
  const currentWholesale = p.done.get(SETTING_STEPS.wholesale);
  const wholesaleFirst = s.retail !== undefined && currentWholesale !== undefined && dec(s.retail).lt(currentWholesale);
  const steps = wholesaleFirst ? [...w, ...r] : [...r, ...w];
  if (s.minStock !== undefined) {
    const minStock = s.minStock;
    steps.push({
      id: SETTING_STEPS.minStock,
      label: "lưu tồn tối thiểu",
      value: num(minStock),
      run: async () => {
        const sku = await client.get(`/skus/${skuId}`, { schema: skuVersionSchema });
        return client.patch(`/skus/${skuId}`, { min_stock: minStock }, { ifMatch: etagOf(sku.version) });
      },
    });
  }
  return steps;
}

const draftValue = (name: string, recipe: RecipeRequest) => JSON.stringify({ name, recipe });

/** Replaces the stored draft's name and recipe when the form changed them after it was stored. */
function draftStep(client: Client, p: Saved, name: string, recipe: RecipeRequest): ValueStep {
  return {
    id: "draft",
    label: "lưu tên và thành phần vừa sửa",
    value: draftValue(name, recipe),
    run: async () => {
      p.kit = await client.patch(`/kits/${p.kit.product_id}`, { name, recipe }, { ifMatch: etagOf(p.kit.version), schema: kitSchema });
    },
  };
}

/** Publishes under `idempotencyKey`, so a retry after a lost answer gets the first answer back. */
const publish = (client: Client, kit: Pick<Kit, "product_id" | "version">, idempotencyKey: string) =>
  client.post(`/kits/${kit.product_id}:publish`, {}, { idempotencyKey, ifMatch: etagOf(kit.version), schema: kitSchema });

function publishStep(client: Client, p: Saved, label: string): ValueStep {
  return {
    id: "publish",
    label,
    value: "",
    run: async () => {
      // A key of its own: the server binds a key to the path, and the intent's other writes use theirs.
      p.kit = await publish(client, p.kit, `${p.key}:publish`);
    },
  };
}

/* ---------- kits ---------- */

export interface CreateKitVars {
  request: KitCreateRequest;
  publish: boolean;
}

/**
 * kitModal (new), kitSaveDraft: creates the kit with its first recipe, prices and minimum stock in
 * one request and, unless saved as a draft, publishes it. `stored` turns true once the kit exists,
 * so the form can lock what a retry no longer changes (type and code).
 */
export function useCreateKit(after: (kit: Kit, publish: boolean) => void, setFieldError?: (field: string, message: string, reason: string) => void) {
  const client = useApiClient();
  const progress = useProgress();
  const [stored, setStored] = useState(false);
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: async (v: CreateKitVars, { idempotencyKey }) => {
      const p = progress.for(idempotencyKey);
      if (!p.kit) {
        const { result } = await lost.watch(p.create, () => kitCall(() => createOnce(p.create, v.request, { send: (body) => client.post("/kits", body satisfies components["schemas"]["KitRequest"], { idempotencyKey, schema: kitSchema }) })));
        // The kit holds what the request that created it asked for; the steps below send the rest.
        const made = p.create.sent ?? v.request;
        p.kit = result;
        p.done.set("draft", draftValue(made.name, made.recipe));
        seed(p, skuSettings(made.retail_price, made.wholesale_price, made.min_stock));
        setStored(true);
      }
      // Narrowed by the write above; the closures below read the latest version of the kit.
      const s = p as Saved;
      // Type, code and unit are fixed once the kit exists: an earlier, unanswered attempt may have
      // created it before the form changed them.
      if (s.kit.type !== v.request.type || s.kit.sku_code !== v.request.sku_code || s.kit.base_unit_id !== v.request.base_unit_id) {
        throw new PartialCommitError(KIT_MESSAGES.createdAsSent(s.kit.name, s.kit.sku_code));
      }
      const steps = [
        draftStep(client, s, v.request.name, v.request.recipe),
        // Only a value the form changed after the kit was created is still to write.
        ...skuSteps(client, idempotencyKey, s.kit.sku_id, skuSettings(v.request.retail_price, v.request.wholesale_price, v.request.min_stock), p),
        ...(v.publish ? [publishStep(client, s, "phát hành")] : []),
      ];
      await runSteps(steps, p, (missing, reason) => `Đã lưu nháp ${s.kit.name} nhưng chưa ${missing}: ${reason}`);
      return s.kit;
    },
    success: (kit, v) =>
      v.publish
        ? withBold("Đã tạo và phát hành ", kit.name, v.request.type === "kit" ? ". Tạo lệnh đóng để có tồn giỏ." : ". Bán được ngay tại POS.")
        : withBold("Đã lưu nháp ", kit.name, ". Xem trước rồi phát hành khi chốt mẫu."),
    invalidate: KIT_KEYS,
    after: (kit, v) => {
      progress.clear();
      setStored(false);
      after(kit, v.publish);
    },
    ...(setFieldError ? { setFieldError } : {}),
  });
  return { ...commit, stored, unsettled: lost.unsettled };
}

export interface UpdateKitVars {
  kit: Kit;
  name: string;
  recipe: RecipeRequest;
  /** The settings the form asks for. */
  settings: KitSkuSettings;
  /** The settings the SKU holds now; one the form leaves unchanged is not written. */
  current: KitSkuSettings;
}

/** kitModal (edit): replaces the draft's name and recipe, then the SKU settings that changed. */
export function useUpdateKitDraft(after: (kit: Kit) => void) {
  const client = useApiClient();
  const progress = useProgress();
  return useCommit({
    mutationFn: async (v: UpdateKitVars, { idempotencyKey }) => {
      const p = progress.for(idempotencyKey);
      if (!p.kit) {
        seed(p, v.current);
        p.kit = await kitCall(() => client.patch(`/kits/${v.kit.product_id}`, { name: v.name, recipe: v.recipe }, { ifMatch: etagOf(v.kit.version), schema: kitSchema }));
        p.done.set("draft", draftValue(v.name, v.recipe));
      }
      const s = p as Saved;
      const steps = [draftStep(client, s, v.name, v.recipe), ...skuSteps(client, idempotencyKey, s.kit.sku_id, v.settings, p)];
      await runSteps(steps, p, (missing, reason) => `Đã lưu nháp ${s.kit.name} nhưng chưa ${missing}: ${reason}`);
      return s.kit;
    },
    success: (kit) => withBold("Đã lưu nháp ", kit.name, "."),
    invalidate: KIT_KEYS,
    after: (kit) => {
      progress.clear();
      after(kit);
    },
  });
}

/**
 * kitPublish: the draft becomes sellable (and, for a kit, can be assembled); a draft version of a
 * kit on sale replaces the selling version.
 */
export function usePublishKit(after?: () => void) {
  const client = useApiClient();
  return useCommit({
    // The key is the kit's own: a key stays bound to the path it was first sent on, and the page's
    // other kits may be published while one answer is lost.
    mutationFn: (v: Pick<Kit, "product_id" | "version" | "name">, { idempotencyKey }) =>
      kitCall(() => publish(client, v, `${idempotencyKey}:${v.product_id}`)),
    success: (kit, v) =>
      kit.latest.version_no > 1
        ? withBold("Đã phát hành ", `v${kit.latest.version_no}`, `. ${v.name} dùng phiên bản này từ bây giờ.`)
        : `Đã phát hành. ${v.name} bán được từ bây giờ.`,
    invalidate: KIT_KEYS,
    after: () => after?.(),
  });
}

/** kitDel: removes a draft that was never published. */
export function useDeleteKit(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: Pick<Kit, "product_id" | "version">) => kitCall(() => client.delete(`/kits/${v.product_id}`, { ifMatch: etagOf(v.version) })),
    success: () => "Đã xóa nháp.",
    invalidate: KIT_KEYS,
    after,
  });
}

export interface NewVersionVars {
  kit: Kit;
  recipe: RecipeRequest;
  /** Combo edit: the retail price the form asks for. */
  retail?: Money;
  /** Combo edit: the prices the SKU holds now. */
  current?: KitSkuSettings;
}

/**
 * recipeVer, comboEdit: adds a recipe version and publishes it straight away, so new assembly
 * orders and sales use it while earlier ones keep theirs. A combo edit may also change the price.
 */
export function useNewRecipeVersion(kind: KitType, after: () => void) {
  const client = useApiClient();
  const today = useBusinessDate();
  const progress = useProgress();
  return useCommit({
    mutationFn: async (v: NewVersionVars, { idempotencyKey }) => {
      const p = progress.for(idempotencyKey);
      if (!p.kit) {
        seed(p, v.current ?? {});
        p.kit = await kitCall(() => client.post(`/kits/${v.kit.product_id}/recipes`, v.recipe, { ifMatch: etagOf(v.kit.version), schema: kitSchema }));
        p.done.set("draft", draftValue(v.kit.name, v.recipe));
      }
      const s = p as Saved;
      const no = s.kit.latest.version_no;
      // A published version is final: components edited after it went out need a version of their own.
      if (p.done.has("publish") && p.done.get("draft") !== draftValue(v.kit.name, v.recipe)) {
        throw new PartialCommitError(`Đã phát hành v${no}; thành phần sửa sau đó chưa được lưu. Đóng hộp thoại rồi tạo phiên bản mới để đổi tiếp.`);
      }
      const steps = [
        draftStep(client, s, v.kit.name, v.recipe),
        publishStep(client, s, "phát hành phiên bản mới"),
        ...skuSteps(client, idempotencyKey, s.kit.sku_id, v.retail !== undefined ? { retail: v.retail } : {}, p),
      ];
      await runSteps(steps, p, (missing, reason) => `Đã lưu v${no} nhưng chưa ${missing}: ${reason}`);
      return s.kit;
    },
    success: (kit) => (kind === "combo" ? "Đã cập nhật combo." : withBold("Đã tạo ", `v${kit.latest.version_no}`, `, có hiệu lực từ ${fd(today)}.`)),
    invalidate: KIT_KEYS,
    after: () => {
      progress.clear();
      after();
    },
  });
}

/* ---------- assembly ---------- */

/** newASM: plans an order; planning holds and consumes nothing. */
export function useCreateAssembly(after: () => void) {
  const client = useApiClient();
  const progress = useRef<{ key: string; create: CreateState<AssemblyRequest, AssemblyDetail> } | null>(null);
  const lost = useUnsettled();
  const commit = useCommit({
    mutationFn: async (v: AssemblyRequest, { idempotencyKey }) => {
      if (progress.current?.key !== idempotencyKey) progress.current = { key: idempotencyKey, create: newCreateState() };
      const create = progress.current.create;
      const { result, edited } = await lost.watch(create, () => kitCall(() => createOnce(create, v, { send: (body) => client.post("/assembly-orders", body, { idempotencyKey, schema: assemblyDetailSchema }) })));
      if (edited) throw new PartialCommitError(KIT_MESSAGES.assemblyCreatedAsSent(result.doc_no));
      return result;
    },
    success: (o) => withBold("Đã tạo ", o.doc_no, "."),
    invalidate: [kitKeys.all],
    after: () => {
      progress.current = null;
      after();
    },
  });
  return { ...commit, unsettled: lost.unsettled };
}

/** asmReserve: holds the components the order still needs. */
export function useReserveAssembly() {
  const client = useApiClient();
  return usePerRowCommit(
    useCommit({
      mutationFn: (v: Pick<AssemblyOrder, "id">, { idempotencyKey }) =>
        kitCall(() => client.post(`/assembly-orders/${v.id}:reserve`, {}, { idempotencyKey, schema: assemblyDetailSchema })),
      success: () => "Đã chốt lệnh và giữ nguyên liệu.",
      invalidate: POSTING_KEYS,
    }),
    (v) => v.id,
  );
}

/** asmDone: issues the components FEFO and receives the finished kits in one posting. */
export function usePostAssembly(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: { id: string; producedQty: Qty; lines?: BatchLineRequest[] }, { idempotencyKey }) =>
      kitCall(() =>
        client.post(`/assembly-orders/${v.id}/postings`, { produced_qty: v.producedQty, ...(v.lines ? { lines: v.lines } : {}) }, { idempotencyKey, schema: postingResultSchema }),
      ),
    success: () => "Đã nhập giỏ thành phẩm.",
    invalidate: POSTING_KEYS,
    after,
  });
}

/** newDIS: takes kits of one lot apart; components go back to the lots they came from. */
export function useDisassemble(after: () => void) {
  const client = useApiClient();
  return useCommit({
    mutationFn: (v: { orderId: string; body: DisassemblyRequest }, { idempotencyKey }) =>
      kitCall(() => client.post(`/assembly-orders/${v.orderId}/disassemblies`, v.body, { idempotencyKey, schema: disassemblySchema })),
    success: () => "Đã tháo giỏ, thành phần về kho theo lô gốc.",
    invalidate: POSTING_KEYS,
    after,
  });
}
