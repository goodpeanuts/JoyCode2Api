import { useEffect, useState } from 'react';
import { api } from '../api';

// Timestamps from the backend are stored and returned as UTC (SQLite
// `datetime('now')` → "YYYY-MM-DD HH:MM:SS" with no offset). The dashboard
// renders them in the configured display timezone; this hook resolves that
// timezone once and shares it across pages.

const browserTz = () => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

// Module-level cache so multiple pages don't each refetch the setting.
let cachedTz: string | null = null;
let inflight: Promise<string> | null = null;

const resolveTz = async (): Promise<string> => {
  if (cachedTz) return cachedTz;
  if (!inflight) {
    inflight = api.getSettings()
      .then(s => (s.timezone && s.timezone.trim()) || browserTz())
      .catch(() => browserTz())
      .then(tz => { cachedTz = tz; return tz; });
  }
  return inflight;
};

/** Returns the configured display timezone (IANA name), defaulting to the
 *  browser timezone until the setting loads. */
export const useTimezone = (): string => {
  const [tz, setTz] = useState<string>(cachedTz || browserTz());
  useEffect(() => {
    let alive = true;
    resolveTz().then(t => { if (alive) setTz(t); });
    return () => { alive = false; };
  }, []);
  return tz;
};

/** Parse a backend UTC timestamp string into a Date (absolute instant). */
export const parseUTC = (t: string): Date | null => {
  if (!t) return null;
  const d = new Date(t.includes('Z') || t.includes('+') ? t : t + 'Z');
  return isNaN(d.getTime()) ? null : d;
};

/** Format a backend UTC timestamp as "YYYY-MM-DD HH:MM:SS" in the given tz. */
export const formatInTimezone = (t: string, tz: string): string => {
  const d = parseUTC(t);
  if (!d) return t || '-';
  // 'sv-SE' locale yields ISO-like "YYYY-MM-DD HH:MM:SS".
  return new Intl.DateTimeFormat('sv-SE', {
    timeZone: tz,
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
    hour12: false,
  }).format(d);
};

/** Build the "%m-%d %H" bucket key for a Date in the given tz, matching the
 *  backend's DST-aware Go bucketing (created_at.In(loc).Format("01-02 15")). */
export const hourKeyInTimezone = (d: Date, tz: string): string => {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: tz,
    month: '2-digit', day: '2-digit', hour: '2-digit', hour12: false,
  }).formatToParts(d);
  const get = (type: string) => parts.find(p => p.type === type)?.value ?? '00';
  // hourCycle:h23 is implied by hour12:false, but some engines still emit '24'
  // at midnight; normalize to '00' (same wall-clock instant, same date parts).
  let hour = get('hour');
  if (hour === '24') hour = '00';
  return `${get('month')}-${get('day')} ${hour}`;
};
