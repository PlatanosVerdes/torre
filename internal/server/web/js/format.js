// Presentation helpers shared by every component, so a flight reads the same everywhere.

const GO = new Set(['EMB', 'ULL']);
const BAD = new Set(['CAN', 'DES', 'DIV']);
const WARN = new Set(['RET', 'NPR', 'NPT', 'CER']);
const OK = new Set(['INI', 'HOR', 'LND', 'IBK', 'FLY', 'FNL']);
const SMALL = new Set(['de', 'del', 'la', 'las', 'los', 'y']);

export function title(s) {
  return String(s ?? '').toLowerCase()
    .replace(/(^|[\s/(-])(\p{L}+)/gu, (m, a, w) => a + (a && SMALL.has(w) ? w : w[0].toUpperCase() + w.slice(1)));
}

export function norm(s) {
  return String(s ?? '').normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();
}

// Dates are AENA's dd/mm/yyyy in Madrid time, which is also the board's.
export function madridDate(offsetDays = 0) {
  return new Date(Date.now() + offsetDays * 864e5)
    .toLocaleDateString('en-GB', { timeZone: 'Europe/Madrid', day: '2-digit', month: '2-digit', year: 'numeric' });
}

export function dayLabel(date) {
  if (!date) return 'Unknown date';
  if (date === madridDate()) return 'Today';
  if (date === madridDate(1)) return 'Tomorrow';
  if (date === madridDate(-1)) return 'Yesterday';
  const [d, m, y] = date.split('/');
  return new Date(+y, m - 1, +d).toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short' });
}

export function delay(min) {
  const sign = min < 0 ? '-' : '+';
  min = Math.abs(min);
  return min >= 60 ? `${sign}${Math.floor(min / 60)} h ${String(min % 60).padStart(2, '0')}` : `${sign}${min} min`;
}

export function tone(f) {
  if (GO.has(f.status)) return 'go';
  if (BAD.has(f.status)) return 'bad';
  if (WARN.has(f.status) || f.delayMin >= 15 || (f.status === 'BOR' && !f.final)) return 'warn';
  if (OK.has(f.status)) return 'ok';
  return '';
}

export function statusText(f) {
  return f.status === 'RET' && f.delayMin > 0 ? `Delayed ${delay(f.delayMin)}` : f.statusLabel;
}

export function place(f) {
  return title(f.otherCity || f.other) + (f.other ? ` (${f.other})` : '');
}
