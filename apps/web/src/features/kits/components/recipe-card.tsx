import type { ReactNode } from "react";
import type { Money } from "../../../shared/domain/money";
import { fd, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Callout } from "../../../shared/ui/callout";
import { Card } from "../../../shared/ui/card";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { StatusPill } from "../../../shared/ui/status-pill";
import type { ColorDirectory } from "../../catalog";
import { KIT_TYPE_TAGS, marginRatio, marginText, savingsText } from "../api/adapters";
import type { CostView, Kit, Recipe } from "../schemas";
import { hasPendingDraft } from "../view-model";
import { ComponentTable } from "./component-table";

export interface RecipeCardProps {
  kit: Kit;
  /**
   * The version the card shows: the latest one, or the selling one while a newer draft waits.
   * Undefined while that selling version loads.
   */
  shown: Recipe | undefined;
  /** Economics of the shown version; undefined while loading. */
  cost: CostView | undefined;
  /** Price of the kit's SKU (the same for every version). */
  price: Money | null;
  /** The "Phiên bản cũ" line. */
  old: ReactNode;
  colors: ColorDirectory;
  unitOf: (skuId: string) => string;
  busy: boolean;
  onPreview: () => void;
  onEdit: () => void;
  onPublish: () => void;
  onDelete: () => void;
  onAssemble: () => void;
  onNewVersion: () => void;
  onEditCombo: () => void;
}

/**
 * Prototype `gio-qua.cong-thuc` card: a kit or combo with its recipe, its economics and the actions
 * of its state. A draft is previewed, edited, published or deleted; a published kit is assembled or
 * gets a new version, a published combo has its components edited. While a newer version waits as
 * a draft, the card keeps showing the selling version and offers that draft apart.
 */
export function RecipeCard(props: RecipeCardProps) {
  const { kit, shown, cost, price, old, colors, unitOf, busy, onPreview, onEdit, onPublish, onDelete, onAssemble, onNewVersion, onEditCombo } = props;
  const isKit = kit.type === "kit";
  const draft = kit.status === "draft";
  const pending = hasPendingDraft(kit);
  const r = shown ?? kit.latest;
  const next = kit.latest.version_no;
  return (
    <Card
      title={isKit ? `${kit.name} · v${pending ? (kit.active_version ?? r.version_no) : r.version_no}` : kit.name}
      actions={
        <ButtonRow>
          <StatusPill label={draft ? "Nháp" : "Đang bán"} />
          <span className={isKit ? "tag" : "tag alt"}>{KIT_TYPE_TAGS[kit.type]}</span>
        </ButtonRow>
      }
    >
      <p className="hint">
        Mã {kit.sku_code}
        {isKit ? ` · hiệu lực từ ${fd(r.valid_from)} · ${r.note ?? ""}` : " · không có tồn riêng"}
        {draft ? (
          <>
            {" · "}
            <b>chưa bán được cho tới khi phát hành</b>
          </>
        ) : null}
      </p>
      {cost && shown ? (
        <>
          <ComponentTable caption={`Thành phần ${kit.name}`} cost={cost} colors={colors} unitOf={unitOf} />
          <ImpactSummary
            items={[
              { label: "Giá bán giỏ", value: vnd(price) },
              { label: "Tổng giá bán lẻ thành phần", value: vnd(cost.components_sale) },
              { label: "Khách tiết kiệm", value: savingsText(price, cost.components_sale, "Không") },
              { label: "Lãi gộp dự kiến", value: marginText(marginRatio(price, cost.total_cost)) },
            ]}
          />
        </>
      ) : (
        <p className="hint">Đang tải…</p>
      )}
      {pending ? (
        <Callout tone="warn" style={{ marginTop: 14 }}>
          <b>
            Đang bán v{kit.active_version} · có nháp v{next}
          </b>{" "}
          chưa phát hành. {isKit ? "Lệnh đóng mới và bán hàng" : "Bán hàng"} vẫn dùng v{kit.active_version} cho tới khi phát hành bản nháp.
          <ButtonRow style={{ marginTop: 8 }}>
            <Button variant="navy" size="sm" onClick={onPreview}>
              Xem trước v{next}
            </Button>
            <Button size="sm" onClick={onEdit}>
              Sửa nháp
            </Button>
            <Button variant="primary" size="sm" onClick={onPublish} disabled={busy}>
              Phát hành v{next}
            </Button>
          </ButtonRow>
        </Callout>
      ) : null}
      <ButtonRow style={{ marginTop: 14 }}>
        {draft ? (
          <>
            <Button variant="navy" size="sm" onClick={onPreview}>
              Xem trước
            </Button>
            <Button size="sm" onClick={onEdit}>
              Sửa nháp
            </Button>
            <Button variant="primary" size="sm" onClick={onPublish} disabled={busy}>
              Phát hành
            </Button>
            <Button variant="danger" size="sm" onClick={onDelete}>
              Xóa nháp
            </Button>
          </>
        ) : (
          <>
            {pending ? null : (
              <Button size="sm" onClick={onPreview}>
                Xem trước
              </Button>
            )}
            {isKit ? (
              <>
                <Button variant="navy" size="sm" onClick={onAssemble}>
                  Tạo lệnh đóng
                </Button>
                {/* A second draft is refused while one waits: edit or publish that one. */}
                {pending ? null : (
                  <Button size="sm" onClick={onNewVersion}>
                    Tạo phiên bản mới
                  </Button>
                )}
              </>
            ) : pending ? null : (
              <Button size="sm" onClick={onEditCombo}>
                Sửa thành phần
              </Button>
            )}
          </>
        )}
      </ButtonRow>
      {old}
    </Card>
  );
}
