// NodeHive displays money in INR by default. Backend cost figures are computed in
// USD (rate cards). We convert at a single, centralized display rate so the whole
// product reads in ₹ without touching the backend. The one place USD is allowed is
// rate-card configuration — the explicitly-configured currency.
//
// To switch the display currency later, change these two constants (or wire them to
// org settings); every page formats through formatMoney/formatMoneyCompact.

export const DISPLAY_CURRENCY = "INR";
export const USD_TO_INR = 83;

const inr = new Intl.NumberFormat("en-IN", {
  style: "currency",
  currency: "INR",
  maximumFractionDigits: 0,
});
const inrPrecise = new Intl.NumberFormat("en-IN", {
  style: "currency",
  currency: "INR",
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

/** Format a USD amount as INR (e.g. 49.62 → "₹4,119"). */
export function formatMoney(usd: number, opts?: { precise?: boolean }): string {
  const value = (usd || 0) * USD_TO_INR;
  return opts?.precise ? inrPrecise.format(value) : inr.format(value);
}

/** Compact INR for tight stat cards (e.g. 1234567 → "₹12.3L"). Input is USD. */
export function formatMoneyCompact(usd: number): string {
  const v = (usd || 0) * USD_TO_INR;
  if (v >= 1e7) return `₹${(v / 1e7).toFixed(2)}Cr`;
  if (v >= 1e5) return `₹${(v / 1e5).toFixed(2)}L`;
  if (v >= 1e3) return `₹${(v / 1e3).toFixed(1)}K`;
  return inr.format(v);
}

/** Format an INR amount that is already in rupees (not USD) — for the credits UI. */
export function formatRupees(rupees: number): string {
  return inr.format(rupees || 0);
}
