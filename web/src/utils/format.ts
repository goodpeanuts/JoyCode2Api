// Shared display helpers so the same underlying number renders identically
// across the dashboard, account list, and account detail pages.

/** Format a token count: >=1M → "X.XXM", >=1K → "X.XK", else grouped digits. */
export const formatTokens = (n: number): string => {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
  return n.toLocaleString();
};

/** Color band for an average latency in ms, tuned for proxy request latencies. */
export const latencyColor = (ms: number): string => {
  if (ms < 500) return '#52c41a';
  if (ms < 1500) return '#faad14';
  return '#ff4d4f';
};

/** Render a percentage that never collapses a small nonzero share to "0%". */
export const formatPercent = (numerator: number, denominator: number): string => {
  if (denominator <= 0) return '0%';
  const pct = (numerator / denominator) * 100;
  if (pct > 0 && pct < 1) return '<1%';
  return `${Math.round(pct)}%`;
};
