import { addDays } from "../../../shared/lib/dates";
import { fd } from "../../../shared/lib/format";
import { Grid } from "../../../shared/ui/grid";
import { KpiCard } from "../../../shared/ui/kpi-card";
import { StatusPill } from "../../../shared/ui/status-pill";
import type { StockCard } from "../schemas";
import { reconcileLabel } from "../view-model";

export interface StockCardKpisProps {
  card: StockCard;
  /** " · màu đỏ", " · chưa gán màu" or "". */
  colorName: string;
  /** Moves in and out over the period, when every row is loaded. */
  moves: { in: number; out: number } | null;
  /** The period reaches today, so the closing can be held against the stock on hand. */
  showCheck: boolean;
}

/** Prototype stock-card KPIs: opening, in, out, closing of the period. */
export function StockCardKpis({ card, colorName, moves, showCheck }: StockCardKpisProps) {
  const big = (text: string) => <span style={{ fontSize: 22 }}>{text}</span>;
  return (
    <Grid cols="g4">
      <KpiCard label="Tồn đầu kỳ" value={big(card.opening.display)} sub={`đến hết ${fd(addDays(card.from, -1))}${colorName}`} />
      <KpiCard label="Nhập trong kỳ" value={big(card.in.display)} sub={moves ? `${moves.in} lần nhập` : undefined} />
      <KpiCard label="Xuất trong kỳ" value={big(card.out.display)} sub={moves ? `${moves.out} lần xuất` : undefined} />
      <KpiCard
        label="Tồn cuối kỳ"
        value={big(card.closing.display)}
        sub={
          <>
            đến hết {fd(card.to)} {showCheck || !card.reconciled ? <StatusPill label={reconcileLabel(card)} /> : null}
          </>
        }
      />
    </Grid>
  );
}
