"use client";
import {
  AreaChart,
  Area,
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import { TONE_HEX } from "./status";

export interface Series {
  key: string;
  name: string;
  tone?: keyof typeof TONE_HEX;
}

const AXIS = "#71717A";
const GRID = "#26262A";

const tooltipStyle = {
  background: "#161618",
  border: "1px solid #2A2A2E",
  borderRadius: 8,
  fontSize: 12,
  boxShadow: "0 8px 28px -6px rgb(0 0 0 / 0.6)",
  color: "#F4F4F5",
};

/**
 * Minimal, flat utilization/trend chart. No gradients, no glow — a thin line over
 * a faint fill, hairline horizontal grid only. `data` rows are { time, [key]: number }.
 */
export function UtilizationChart({
  data,
  series,
  height = 200,
  unit = "%",
  domain = [0, 100],
  variant = "area",
}: {
  data: Record<string, number | string>[];
  series: Series[];
  height?: number;
  unit?: string;
  domain?: [number, number];
  variant?: "area" | "line";
}) {
  const Chart = variant === "line" ? LineChart : AreaChart;
  return (
    <ResponsiveContainer width="100%" height={height}>
      <Chart data={data} margin={{ top: 6, right: 6, left: -18, bottom: 0 }}>
        <CartesianGrid stroke={GRID} vertical={false} />
        <XAxis dataKey="time" stroke={AXIS} tick={{ fontSize: 11, fill: AXIS }} tickLine={false} axisLine={{ stroke: GRID }} minTickGap={28} />
        <YAxis stroke={AXIS} tick={{ fontSize: 11, fill: AXIS }} tickLine={false} axisLine={false} domain={domain} unit={unit} width={44} />
        <Tooltip contentStyle={tooltipStyle} labelStyle={{ color: "#A1A1AA", marginBottom: 2 }} cursor={{ stroke: "#3A3A40" }} />
        {series.map((s) =>
          variant === "line" ? (
            <Line key={s.key} type="monotone" dataKey={s.key} name={s.name} stroke={TONE_HEX[s.tone ?? "neutral"]} strokeWidth={2} dot={false} />
          ) : (
            <Area
              key={s.key}
              type="monotone"
              dataKey={s.key}
              name={s.name}
              stroke={TONE_HEX[s.tone ?? "neutral"]}
              strokeWidth={2}
              fill={TONE_HEX[s.tone ?? "neutral"]}
              fillOpacity={0.06}
            />
          ),
        )}
      </Chart>
    </ResponsiveContainer>
  );
}
