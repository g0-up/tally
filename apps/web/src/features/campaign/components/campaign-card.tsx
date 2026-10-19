import { useState } from "react";
import { errorMessage } from "../../../shared/api/api-error";
import { fd, num, vnd } from "../../../shared/lib/format";
import { Button, ButtonRow } from "../../../shared/ui/button";
import { Card } from "../../../shared/ui/card";
import { Checkbox } from "../../../shared/ui/checkbox";
import { ImpactSummary } from "../../../shared/ui/impact-summary";
import { ListLoading } from "../../../shared/ui/list-footer";
import { Progress } from "../../../shared/ui/progress";
import { StatusPill } from "../../../shared/ui/status-pill";
import { CAMPAIGN_STATUS_LABELS, seasonDays, seasonFigure } from "../api/adapters";
import { useActivateCampaign, useToggleChecklistItem } from "../api/mutations";
import { useChecklist, useSeasonReport } from "../api/queries";
import type { Campaign } from "../schemas";

const NONE = "—";

export interface CampaignCardProps {
  campaign: Campaign;
  /** Business date, for the days of the season gone by. */
  today: string;
  onClose: (c: Campaign) => void;
}

/** One `.g2` card of the seasons screen: dates, the season's figures, the checklist and its command. */
export function CampaignCard({ campaign: c, today, onClose }: CampaignCardProps) {
  const activate = useActivateCampaign();
  return (
    <Card title={c.name} actions={<StatusPill label={CAMPAIGN_STATUS_LABELS[c.status]} />}>
      <p className="hint">
        {fd(c.starts_on)} – {fd(c.ends_on)}
      </p>
      <SeasonSummary campaign={c} today={today} />
      <CampaignChecklist campaign={c} />
      {c.status !== "closed" ? (
        <ButtonRow style={{ marginTop: 16 }}>
          {c.status === "planning" ? (
            <Button variant="navy" size="sm" pending={activate.pending} aria-label={`Bắt đầu mùa ${c.name}`} onClick={() => void activate.run(c)}>
              Bắt đầu mùa
            </Button>
          ) : (
            <Button size="sm" aria-label={`Kết mùa ${c.name}`} onClick={() => onClose(c)}>
              Kết mùa
            </Button>
          )}
        </ButtonRow>
      ) : null}
    </Card>
  );
}

/**
 * Prototype campSummary on the season report: net revenue against the target, gross margin, the
 * budget, the orders still to deliver and what customers owe on them. The API records no season
 * expenses, so "Chi phí mùa" and "Kết quả quản trị" read "—".
 */
function SeasonSummary({ campaign: c, today }: { campaign: Campaign; today: string }) {
  const report = useSeasonReport(c.id);
  if (report.isPending) return <ListLoading />;
  if (report.error) {
    return (
      <p className="hint" role="alert">
        Không tải được số liệu mùa: {errorMessage(report.error)}
      </p>
    );
  }
  const net = seasonFigure(report.data, "net_revenue");
  const target = seasonFigure(report.data, "target_revenue");
  const achieved = seasonFigure(report.data, "revenue_achieved");
  const margin = seasonFigure(report.data, "gross_margin");
  const budget = seasonFigure(report.data, "budget");
  const open = seasonFigure(report.data, "open_orders");
  const owed = seasonFigure(report.data, "receivable");
  const { elapsed, total } = seasonDays(c.starts_on, c.ends_on, today);
  return (
    <div className="stack" style={{ gap: 14 }}>
      <div>
        <div className="btn-row" style={{ justifyContent: "space-between" }}>
          <span className="hint">Doanh thu thuần / mục tiêu</span>
          <b className="tab-n" style={{ color: "var(--navy-900)" }}>
            {net === null ? NONE : vnd(net)} / {target === null ? NONE : vnd(target)}
          </b>
        </div>
        <div style={{ marginTop: 6 }}>
          <Progress value={net ?? 0} max={target ?? 0} label="Doanh thu thuần / mục tiêu" accent />
        </div>
        <span className="hint">
          {achieved === null ? "Chưa đặt doanh thu mục tiêu" : `${num(achieved.toDecimalPlaces(1))}%`} sau {elapsed}/{total} ngày của mùa
        </span>
      </div>
      <ImpactSummary
        items={[
          { label: "Lãi gộp", value: margin === null ? NONE : vnd(margin) },
          { label: "Chi phí mùa", value: `${NONE} / ngân sách ${budget === null ? NONE : vnd(budget)}` },
          { label: "Kết quả quản trị", value: NONE },
          { label: "Đơn còn phải giao", value: open === null ? NONE : vnd(open) },
          { label: "Khách còn nợ từ đơn mùa", value: owed === null ? NONE : vnd(owed) },
        ]}
      />
    </div>
  );
}

/** "Checklist trước cao điểm & kết mùa": each tick is saved at once; a closed season is read-only. */
function CampaignChecklist({ campaign: c }: { campaign: Campaign }) {
  const checklist = useChecklist(c.id);
  const [errors, setErrors] = useState<Readonly<Record<string, string>>>({});
  const toggle = useToggleChecklistItem(c.id, (itemId, message) => setErrors((prev) => ({ ...prev, [itemId]: message })));
  const [savingId, setSavingId] = useState<string | null>(null);
  const items = [...(checklist.data?.data ?? [])].sort((a, b) => a.sort_order - b.sort_order);
  if (checklist.error) {
    return (
      <p className="hint" role="alert">
        Không tải được checklist: {errorMessage(checklist.error)}
      </p>
    );
  }
  if (checklist.isPending || items.length === 0) return null;
  return (
    <>
      <h3 className="sec-h" style={{ margin: "16px 0 8px" }}>
        Checklist trước cao điểm & kết mùa
      </h3>
      {items.map((x) => {
        const error = errors[x.id];
        // The item being saved keeps focus and reports itself busy; the others wait for it, since
        // each answer carries the whole checklist.
        const saving = toggle.pending && savingId === x.id;
        return (
          <div key={x.id}>
            <Checkbox
              label={x.title}
              checked={x.done}
              disabled={c.status === "closed" || (toggle.pending && !saving)}
              aria-busy={saving || undefined}
              aria-invalid={error !== undefined}
              onChange={(e) => {
                if (saving) return;
                setErrors((prev) => Object.fromEntries(Object.entries(prev).filter(([id]) => id !== x.id)));
                setSavingId(x.id);
                void toggle.run({ itemId: x.id, done: e.target.checked });
              }}
            />
            {error !== undefined ? (
              <span className="err-text" role="alert">
                {error}
              </span>
            ) : null}
          </div>
        );
      })}
    </>
  );
}
