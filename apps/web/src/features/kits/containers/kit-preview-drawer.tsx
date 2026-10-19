import { useMemo } from "react";
import { dec } from "../../../shared/lib/decimal";
import { num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { ColorChip } from "../../../shared/ui/color/color-chip";
import { DetailDrawer, DrawerError } from "../../../shared/ui/detail-drawer";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { baseUnitName, useColorDirectory, useSkusByIds } from "../../catalog";
import { marginRatio, marginText, savingsText } from "../api/adapters";
import { usePublishKit } from "../api/mutations";
import { useKit, useKitCosts } from "../api/queries";
import { ComponentTable } from "../components/component-table";
import type { Kit } from "../schemas";
import { hasPendingDraft, isMaterial } from "../view-model";

export interface KitPreviewDrawerProps {
  kitId: string;
  onClose: () => void;
  onEdit: (kit: Kit) => void;
}

/**
 * Prototype `kitPreview`: the latest version of the kit as a customer sees it (price, saving,
 * contents, packing, wholesale price) and, below, its internal economics. A draft version, of a
 * new kit or of one on sale, can be published or edited from here.
 */
export function KitPreviewDrawer({ kitId, onClose, onEdit }: KitPreviewDrawerProps) {
  const kitQuery = useKit(kitId);
  const kit = kitQuery.data;
  const cost = useKitCosts(useMemo(() => [kitId], [kitId])).get(kitId);
  const ids = useMemo(() => (kit ? [kit.sku_id, ...(cost?.components.map((c) => c.sku_id) ?? [])] : []), [kit, cost]);
  const skus = useSkusByIds(ids).data;
  const colors = useColorDirectory();
  const publish = usePublishKit();

  if (kitQuery.isError) {
    return (
      <DetailDrawer open onClose={onClose} title="Xem trước">
        <DrawerError message="Không tải được giỏ/combo." />
      </DetailDrawer>
    );
  }
  if (!kit) {
    return (
      <DetailDrawer open onClose={onClose} title="Xem trước">
        <p className="hint">Đang tải…</p>
      </DetailDrawer>
    );
  }

  const isKit = kit.type === "kit";
  const waiting = hasPendingDraft(kit);
  const draft = kit.status === "draft" || waiting;
  const sku = skus?.get(kit.sku_id);
  const price = cost?.kit_price ?? sku?.retail_price ?? null;
  const retail = cost?.components_sale ?? "0";
  const unitOf = (id: string) => {
    const s = skus?.get(id);
    return s ? baseUnitName(s) : "";
  };
  const shown = cost?.components.filter((c) => !isMaterial(c.kind)) ?? [];
  const materials = cost?.components.filter((c) => isMaterial(c.kind)) ?? [];
  const ratio = cost ? marginRatio(price, cost.total_cost) : null;

  return (
    <DetailDrawer
      open
      onClose={onClose}
      title={`Xem trước · ${kit.name}`}
      sub={`${waiting ? `Bản nháp v${kit.latest.version_no}, đang bán v${kit.active_version}` : draft ? "Bản nháp, chưa bán được" : "Đang bán"} · ${isKit ? "giỏ đóng sẵn" : "combo ghép khi bán"}`}
    >
      {draft ? (
        <ButtonRow>
          <Button variant="primary" size="sm" disabled={publish.pending} onClick={() => void publish.run(kit)}>
            {waiting ? `Phát hành v${kit.latest.version_no}` : "Phát hành"}
          </Button>
          <Button size="sm" onClick={() => onEdit(kit)}>
            Sửa nháp
          </Button>
        </ButtonRow>
      ) : null}
      <div className="preview-card">
        <div className="pv-top">
          <span className="tag">{isKit ? "Giỏ quà Tết" : "Combo"}</span>
          {draft ? <span className="tag alt">Bản nháp</span> : null}
        </div>
        <h4>{kit.name}</h4>
        <div className="pv-price">{vnd(price)}</div>
        {price !== null && dec(retail).gt(price) ? (
          <div className="pv-save">
            Mua lẻ {vnd(retail)} · tiết kiệm {savingsText(price, retail, "")}
          </div>
        ) : null}
        <ul>
          {shown.map((c, i) => (
            <li key={`${c.line_no}-${c.sku_id}-${i}`}>
              <b>
                {num(c.base_qty)} {unitOf(c.sku_id)}
              </b>{" "}
              {c.name}
              {c.color_id ? (
                <>
                  {" "}
                  <ColorChip color={colors.codeOf(c.color_id)} />
                </>
              ) : null}
            </li>
          ))}
        </ul>
        {materials.length > 0 ? <p className="hint">Đóng gói: {materials.map((c) => c.name).join(", ")}</p> : null}
        <p className="hint">Giá doanh nghiệp / sỉ: {vnd(sku?.wholesale_price ?? null)}</p>
      </div>
      <h3 className="sec-h">Thông tin nội bộ</h3>
      {cost ? (
        <>
          <ImpactSummary
            items={[
              { label: "Giá vốn dự kiến", value: vnd(cost.total_cost) },
              { label: "Lãi gộp / giỏ", value: price === null ? "—" : `${vnd(dec(price).minus(cost.total_cost))} (${marginText(ratio)})` },
              { label: "Giá tối thiểu", value: vnd(sku?.min_price ?? null) },
              { label: isKit ? "Có thể đóng" : "Có thể bán", value: `${num(cost.buildable)} ${isKit ? "giỏ" : "combo"}` },
            ]}
          />
          <ComponentTable caption={`Thành phần ${kit.name}`} cost={cost} colors={colors} unitOf={unitOf} />
        </>
      ) : (
        <p className="hint">Đang tải…</p>
      )}
    </DetailDrawer>
  );
}
