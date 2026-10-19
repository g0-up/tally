import { Fragment } from "react";
import { sumDec } from "../../lib/decimal";
import { axisLabel, fd, fdS, money, vnd } from "../../lib/format";
import { DataTable } from "../data-table/data-table";
import type { Column } from "../data-table/columns";
import { ChartFrame, type ChartHit } from "./chart-frame";
import { amount, roundedTopPath, yScale, type ChartPoint, type ChartSeries } from "./chart-math";

const W = 640;
const H = 230;
const L = 58;
const B = 26;
const T = 12;
const R = 8;

export interface BarChartProps<K extends string> {
  data: ReadonlyArray<ChartPoint<K>>;
  series: ReadonlyArray<ChartSeries<K>>;
  /** X axis label; dd/mm by default. */
  xLabel?: (x: string) => string;
  ariaLabel?: string;
  /** Caption of the data table (usually the card title). */
  caption: string;
  xHeader?: string;
}

/**
 * Prototype `barChart()`: stacked daily bars with rounded tops, 4 nice grid steps labelled in
 * "tr"/"k", every second date label when there are more than 14 bars, and a total in the tooltip.
 */
export function BarChart<K extends string>({ data, series, xLabel = fdS, ariaLabel = "Biểu đồ cột chồng doanh thu theo ngày", caption, xHeader = "Ngày" }: BarChartProps<K>) {
  const totals = data.map((r) => series.reduce((acc, s) => acc + amount(r.values[s.key]), 0));
  const sc = yScale(Math.max(1, ...totals), H, T, B);
  const bw = (W - L - R) / Math.max(1, data.length);
  const w = Math.min(30, bw * 0.56);
  const labelEvery = data.length > 14 ? 2 : 1;

  const hits: ChartHit[] = data.map((r, i) => {
    const total = vnd(sumDec(series.map((s) => r.values[s.key])));
    return {
      key: r.x,
      x: L + bw * i,
      y: T,
      width: bw,
      height: H - T - B,
      tipLabel: `${fd(r.x)}: ${series.map((s) => `${s.name} ${vnd(r.values[s.key])}`).join(", ")}, tổng ${total}`,
      tip: (
        <>
          <b>{fd(r.x)}</b>
          {series.map((s) => (
            <Fragment key={s.key}>
              <br />
              {s.name}: {vnd(r.values[s.key])}
            </Fragment>
          ))}
          <br />
          Tổng: {total}
        </>
      ),
    };
  });

  const columns: Column<ChartPoint<K>>[] = [
    { key: "x", header: xHeader, cell: (r) => fd(r.x) },
    ...series.map((s) => ({ key: s.key, header: s.tableHeader ?? s.name, numeric: true, cell: (r: ChartPoint<K>) => money(r.values[s.key]) })),
    { key: "total", header: "Tổng", numeric: true, cell: (r) => money(sumDec(series.map((s) => r.values[s.key]))) },
  ];

  return (
    <ChartFrame
      width={W}
      height={H}
      ariaLabel={ariaLabel}
      hits={hits}
      legend={series.map((s) => ({ name: s.name, color: s.color }))}
      table={<DataTable caption={caption} columns={columns} rows={data} rowKey={(r) => r.x} flat />}
    >
      {sc.ticks.map((v) => (
        <Fragment key={v}>
          <line className="gl" x1={L} x2={W - R} y1={sc.y(v)} y2={sc.y(v)} />
          <text className="ax" x={L - 8} y={sc.y(v) + 4} textAnchor="end">
            {axisLabel(v)}
          </text>
        </Fragment>
      ))}
      {data.map((r, i) => {
        const cx = L + bw * i + bw / 2;
        const segs = series.filter((s) => amount(r.values[s.key]) > 0);
        let acc = 0;
        return (
          <g key={r.x}>
            {segs.map((s, j) => {
              const v = amount(r.values[s.key]);
              const y1 = sc.y(acc + v);
              const y0 = sc.y(acc);
              acc += v;
              const h = Math.max(1, y0 - y1 - (j > 0 ? 2 : 0));
              const isTop = j === segs.length - 1;
              return isTop ? (
                <path key={s.key} fill={s.color} d={roundedTopPath(cx - w / 2, y1, w, h, Math.min(4, h, w / 2))} />
              ) : (
                <rect key={s.key} fill={s.color} x={cx - w / 2} y={y1} width={w} height={h} />
              );
            })}
            {i % labelEvery === 0 ? (
              <text className="ax" x={cx} y={H - 8} textAnchor="middle">
                {xLabel(r.x)}
              </text>
            ) : null}
          </g>
        );
      })}
      <line x1={L} x2={W - R} y1={sc.y(0)} y2={sc.y(0)} stroke="var(--gray-300)" />
    </ChartFrame>
  );
}
