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
  if (f.doubtful) return f.delayMin >= 15 ? 'warn' : 'ok';
  if (GO.has(f.status)) return 'go';
  if (BAD.has(f.status)) return 'bad';
  if (WARN.has(f.status) || f.delayMin >= 15) return 'warn';
  if (OK.has(f.status)) return 'ok';
  return '';
}

export function statusText(f) {
  if ((f.status === 'RET' || f.doubtful) && f.delayMin > 0) return `Delayed ${delay(f.delayMin)}${f.doubtful ? ' *' : ''}`;
  return f.statusLabel;
}

// farEnd is the other airport's name: in English when the server knew the code, else AENA's
// upper-case Spanish name, tidied.
export function farEnd(f) {
  const name = f.otherCity || f.other || '';
  return name === name.toUpperCase() ? title(name) : name;
}

export function place(f) {
  return farEnd(f) + (f.other ? ` (${f.other})` : '');
}

// close is true when word is a typo away from the start of token: one edit for short words, two
// for long ones, a swap of neighbours counting as one ("rayan" finds "ryanair", "barclona" finds
// "barcelona"). Only used when nothing matches exactly, so it never adds noise to a good search.
export function close(word, token) {
  if (word.length < 4) return false;
  const max = word.length >= 7 ? 2 : 1;
  for (let len = word.length - max; len <= word.length + max; len++) {
    if (len > 0 && len <= token.length && distance(word, token.slice(0, len)) <= max) return true;
  }
  return false;
}

// distance is the optimal string alignment distance: edits, with adjacent swaps counted once.
function distance(a, b) {
  const d = Array.from({ length: a.length + 1 }, (_, i) => [i, ...Array(b.length).fill(0)]);
  for (let j = 1; j <= b.length; j++) d[0][j] = j;
  for (let i = 1; i <= a.length; i++) {
    for (let j = 1; j <= b.length; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      d[i][j] = Math.min(d[i - 1][j] + 1, d[i][j - 1] + 1, d[i - 1][j - 1] + cost);
      if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) d[i][j] = Math.min(d[i][j], d[i - 2][j - 2] + 1);
    }
  }
  return d[a.length][b.length];
}
