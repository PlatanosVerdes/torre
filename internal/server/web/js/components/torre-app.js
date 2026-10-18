import { LitElement, html, css } from '../../vendor/lit-3.3.3.js';
import { repeat } from '../../vendor/lit-3.3.3.js';
import { shared } from '../styles.js';
import { close, dayLabel, madridDate, norm } from '../format.js';
import { BoardingPass } from '../boarding-pass.js';
import './flight-row.js';
import './torre-sheet.js';
import './flight-details.js';
import './pass-legs.js';
import './airport-picker.js';

const store = {
  get(k, d) { try { return localStorage.getItem(k) ?? d; } catch { return d; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch {} },
};
const iata = (s) => /^[A-Z]{3}$/.test(s || '') ? s : null;

// A long board renders this many rows; the search narrows it far below that.
const MAX_ROWS = 400;

// The whole page: header, day chips, followed flights, the board and the bottom sheet.
export class TorreApp extends LitElement {
  static properties = {
    airport: { state: true },
    dir: { state: true },
    query: { state: true },
    day: { state: true },
    showDeparted: { state: true },
    full: { state: true },
    flights: { state: true },
    updatedAt: { state: true },
    error: { state: true },
    airports: { state: true },
    tracked: { state: true },
    canFollow: { state: true },
    notify: { state: true },
    bot: { state: true },
    sheet: { state: true },
    busy: { state: true },
    message: { state: true },
    now: { state: true },
  };

  static styles = [shared, css`
    :host { display: block; }
    header {
      position: sticky; top: 0; z-index: 5; background: var(--bg);
      padding: calc(env(safe-area-inset-top) + 10px) 16px 8px; border-bottom: 1px solid var(--line);
    }
    .row { display: flex; gap: 8px; align-items: center; }
    h1 { font-size: 20px; margin: 0; letter-spacing: -0.02em; }
    input[type=search], .tool {
      font: inherit; color: var(--ink); background: var(--card); border: 1px solid var(--line); border-radius: 10px;
    }
    input[type=search] { width: 100%; margin-top: 8px; font-size: 16px; padding: 9px 12px; }
    .tool { width: 100%; margin-top: 8px; font-size: 14px; font-weight: 600; padding: 8px 10px; cursor: pointer; }
    .seg { display: flex; background: var(--soft); border-radius: 10px; padding: 3px; margin-top: 8px; }
    .seg button {
      flex: 1; border: 0; background: transparent; color: var(--muted); font: inherit; font-weight: 600;
      padding: 7px; border-radius: 8px; cursor: pointer;
    }
    .seg button[aria-pressed="true"] { background: var(--card); color: var(--ink); box-shadow: 0 1px 2px rgba(0, 0, 0, .12); }
    .chips { display: flex; gap: 6px; overflow-x: auto; margin-top: 8px; scrollbar-width: none; }
    .chips::-webkit-scrollbar { display: none; }
    .chip {
      flex: none; border: 1px solid var(--line); background: var(--card); color: var(--ink); font: inherit;
      font-size: 13px; padding: 5px 10px; border-radius: 999px; white-space: nowrap; cursor: pointer;
    }
    .chip[aria-pressed="true"] { background: var(--ink); color: var(--bg); border-color: var(--ink); }
    .meta { color: var(--muted); font-size: 12px; padding: 8px 16px 0; display: flex; justify-content: space-between; }
    main { padding: 4px 16px 24px; }
    h2 { font-size: 13px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); margin: 16px 0 6px; }
    .list { background: var(--card); border: 1px solid var(--line); border-radius: 12px; overflow: hidden; }
    .empty, .error { padding: 24px 12px; text-align: center; color: var(--muted); }
    .error { color: var(--bad); }
    .pending { padding: 12px; color: var(--muted); font-size: 14px; border-top: 1px solid var(--line); }
    .pending:first-child { border-top: 0; }
    .pending b { color: var(--ink); }
    .pending button { float: right; border: 0; background: none; color: var(--accent); font: inherit; font-weight: 600; cursor: pointer; }
    .more {
      display: block; width: 100%; margin-top: 10px; padding: 11px; border-radius: 10px; border: 1px dashed var(--line);
      background: none; color: var(--accent); font: inherit; font-weight: 600; cursor: pointer;
    }
    .toast {
      position: fixed; left: 50%; bottom: calc(20px + env(safe-area-inset-bottom)); transform: translateX(-50%);
      background: var(--ink); color: var(--bg); padding: 9px 14px; border-radius: 10px; font-size: 14px; z-index: 20;
      max-width: calc(100% - 32px);
    }
    input[type=file] { display: none; }
  `];

  constructor(api, telegram) {
    super();
    this.api = api;
    this.telegram = telegram;
    const params = new URLSearchParams(location.search);
    const target = telegram.openTarget();
    this.airport = iata(target?.airport) || iata((params.get('a') || '').toUpperCase()) || iata(store.get('airport', '')) || 'BCN';
    this.dir = (target?.dir || params.get('d') || store.get('dir', 'S')).toUpperCase() === 'L' ? 'L' : 'S';
    this.query = params.get('q') || '';
    this.pendingOpen = target?.flight || params.get('f') || '';
    this.day = null;
    this.showDeparted = false;
    this.full = false;
    this.flights = [];
    this.airports = [];
    this.tracked = [];
    this.canFollow = false;
    this.sheet = null;
    this.now = Date.now();
    this.loading = 0;
  }

  connectedCallback() {
    super.connectedCallback();
    this.#saveUrl();
    this.#loadAirports();
    this.#loadTracked().then(() => this.#loadBoard());
    this.onVisible = () => document.visibilityState === 'visible' && this.#refresh();
    document.addEventListener('visibilitychange', this.onVisible);
    this.timers = [setInterval(() => this.onVisible(), 60_000), setInterval(() => { this.now = Date.now(); }, 30_000)];
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    document.removeEventListener('visibilitychange', this.onVisible);
    this.timers.forEach(clearInterval);
  }

  // --- Data -----------------------------------------------------------------------------------

  async #loadAirports() {
    try { this.airports = await this.api.airports(); } catch {}
  }

  async #loadBoard() {
    const my = ++this.loading;
    try {
      const data = await this.api.board(this.airport, this.dir, this.full);
      if (my !== this.loading) return;
      this.flights = data.flights;
      this.updatedAt = data.updated;
      this.error = '';
      this.#pickDay();
      if (this.pendingOpen) {
        const wanted = this.pendingOpen;
        this.pendingOpen = '';
        const f = this.flights.find(f => f.id === wanted || f.number === wanted.toUpperCase());
        if (f) this.#openFlight(f);
      }
    } catch (e) {
      if (my === this.loading) this.error = e.message;
    }
  }

  async #loadTracked() {
    try {
      const data = await this.api.tracked();
      this.tracked = data.tracked || [];
      this.canFollow = data.canFollow;
      this.notify = data.notify;
      this.bot = data.bot || '';
    } catch {}
  }

  #refresh() { this.#loadBoard(); this.#loadTracked(); }

  #pickDay() {
    const days = this.#days();
    if (this.day && days.includes(this.day)) return;
    this.day = days.includes(madridDate()) ? madridDate() : days[0] || null;
  }

  #days() { return [...new Set(this.flights.map(f => f.date))]; }

  // visible is the board filtered by day and search. When the search matches nothing exactly it
  // retries tolerating typos, and fuzzy says so, so the page can tell the user.
  #visible() {
    const compact = norm(this.query).replace(/\s+/g, '');
    const words = norm(this.query).split(/\s+/).filter(Boolean);
    const inDay = this.flights.filter(f => (!this.day || f.date === this.day) && (this.showDeparted || compact || !f.final));
    if (!words.length) return { flights: inDay, fuzzy: false };

    const hay = (f) => norm([f.number, ...(f.codeshare || []), f.airline, f.otherCity, f.other].join(' '));
    const exact = inDay.filter(f => { const h = hay(f); return h.replace(/\s+/g, '').includes(compact) || words.every(w => h.includes(w)); });
    if (exact.length) return { flights: exact, fuzzy: false };

    const tokens = (f) => hay(f).split(/[\s\-/()]+/).filter(Boolean);
    const near = inDay.filter(f => { const t = tokens(f); return words.every(w => t.some(x => x.includes(w) || close(w, x))); });
    return { flights: near, fuzzy: near.length > 0 };
  }

  #saveUrl() {
    store.set('airport', this.airport);
    store.set('dir', this.dir);
    const p = new URLSearchParams({ a: this.airport, d: this.dir });
    if (this.query) p.set('q', this.query);
    // The fragment holds Telegram's launch data; dropping it would sign the user out.
    history.replaceState(null, '', '?' + p + location.hash);
  }

  // --- Actions --------------------------------------------------------------------------------

  #setAirport(code) {
    this.airport = code; this.day = null; this.full = false; this.flights = []; this.error = '';
    this.#saveUrl(); this.#loadBoard();
  }

  #setDir(dir) {
    if (dir === this.dir) return;
    this.dir = dir; this.full = false;
    this.#saveUrl(); this.#loadBoard();
  }

  #openFlight(flight, trackId) {
    const t = this.tracked.find(t => t.id === trackId || (t.last && t.last.id === flight.id && t.airport === flight.airport));
    this.sheet = { kind: 'flight', flight, trackedId: t?.id || '' };
  }

  async #follow(airport, direction, date, number) {
    this.busy = true;
    try {
      const t = await this.api.follow(airport, direction, date, number);
      this.#toast(t.last ? (this.notify ? 'Following. Check Telegram.' : 'Following') : 'Saved. AENA publishes it about two weeks before.');
      this.sheet = null;
      this.#loadTracked();
    } catch (e) {
      this.#toast(e.message);
    } finally {
      this.busy = false;
    }
  }

  async #unfollow(id) {
    try {
      await this.api.unfollow(id);
      this.#toast('Stopped following');
      this.sheet = null;
      this.#loadTracked();
    } catch (e) {
      this.#toast(e.message);
    }
  }

  async #readPass(file) {
    this.#toast('Reading the boarding pass…', 20_000);
    try {
      const legs = await BoardingPass.read(file);
      if (!this.airports.length) await this.#loadAirports();
      const aena = new Set(this.airports.map(a => a.iata));
      this.message = '';
      this.sheet = {
        kind: 'pass',
        legs: legs.map(l => aena.has(l.from) ? { ...l, airport: l.from, direction: 'S' }
          : aena.has(l.to) ? { ...l, airport: l.to, direction: 'L' }
          : { ...l, airport: null }),
      };
    } catch (e) {
      this.#toast(e.message);
    }
  }

  #showLeg(l) {
    this.sheet = null;
    this.pendingOpen = `${l.date}-${l.number}`;
    this.day = l.date;
    if (l.airport === this.airport && l.direction === this.dir) { this.#loadBoard(); return; }
    this.airport = l.airport; this.dir = l.direction; this.full = false;
    this.#saveUrl(); this.#loadBoard();
  }

  #toast(text, ms = 3200) {
    this.message = text;
    clearTimeout(this.toastTimer);
    this.toastTimer = setTimeout(() => { this.message = ''; }, ms);
  }

  // --- Rendering ------------------------------------------------------------------------------

  render() {
    const { flights: visible, fuzzy } = this.#visible();
    return html`
      <header>
        <div class="row">
          <h1>Torre</h1>
          <airport-picker .airports=${this.airports} .value=${this.airport}
            @airport-change=${e => this.#setAirport(e.detail.iata)}></airport-picker>
        </div>
        <div class="seg" role="group" aria-label="Board">
          <button aria-pressed=${this.dir === 'S'} @click=${() => this.#setDir('S')}>Departures</button>
          <button aria-pressed=${this.dir === 'L'} @click=${() => this.#setDir('L')}>Arrivals</button>
        </div>
        <input type="search" .value=${this.query} placeholder="Flight, city or airline (FR2495, Palma, Ryanair)"
          autocomplete="off" enterkeyhint="search" @input=${this.#onSearch}>
        <button class="tool" @click=${() => this.renderRoot.querySelector('input[type=file]').click()}>Add boarding pass</button>
        <input type="file" accept="image/*,.pkpass,application/vnd.apple.pkpass" @change=${this.#onFile}>
        <div class="chips">
          ${this.#days().map(d => html`<button class="chip" aria-pressed=${d === this.day} @click=${() => { this.day = d; }}>${dayLabel(d)}</button>`)}
          <button class="chip" aria-pressed=${this.showDeparted} @click=${() => { this.showDeparted = !this.showDeparted; }}>Show departed</button>
        </div>
      </header>
      <div class="meta"><span>${this.#updatedText()}</span><span>${this.flights.length ? `${visible.length} flights` : ''}</span></div>
      ${fuzzy ? html`<div class="meta">No exact match for “${this.query}”, showing close ones</div>` : ''}
      <main>
        ${this.tracked.length ? html`<h2>Following</h2><div class="list">${this.tracked.map(t => this.#trackedRow(t))}</div>` : ''}
        <h2>${this.dir === 'S' ? 'Departures' : 'Arrivals'} · ${this.airport}</h2>
        <div class="list" @flight-select=${e => this.#openFlight(e.detail.flight, e.detail.trackId)}>${this.#board(visible)}</div>
        ${this.full ? '' : html`<button class="more" @click=${this.#loadFull}>Show more days</button>`}
      </main>
      <torre-sheet ?open=${!!this.sheet} @sheet-close=${() => { this.sheet = null; }}>${this.#sheetContent()}</torre-sheet>
      ${this.message ? html`<div class="toast" role="status">${this.message}</div>` : ''}`;
  }

  #board(visible) {
    if (this.error) return html`<div class="error">${this.error}</div>`;
    if (!this.flights.length) return html`<div class="empty">Loading…</div>`;
    if (!visible.length) return html`<div class="empty">${this.query ? 'No flight matches' : 'No flights'}${this.full ? '' : '. Try “Show more days”.'}</div>`;
    return repeat(visible.slice(0, MAX_ROWS), f => f.id, f => html`<flight-row .flight=${f}></flight-row>`);
  }

  #trackedRow(t) {
    if (t.last) {
      return html`<flight-row .flight=${t.last} .trackId=${t.id} withRoute
        @flight-select=${e => this.#openFlight(e.detail.flight, e.detail.trackId)}></flight-row>`;
    }
    return html`<div class="pending">
      <button @click=${() => this.#unfollow(t.id)}>Remove</button>
      <b>${t.number}</b> · ${t.airport} · ${dayLabel(t.date)}<br>Waiting for AENA to publish it, about two weeks before.
    </div>`;
  }

  #sheetContent() {
    const s = this.sheet;
    if (s?.kind === 'flight') {
      return html`<flight-details .flight=${s.flight} .trackedId=${s.trackedId} ?canFollow=${this.canFollow}
        ?notify=${this.notify} .bot=${this.bot} ?busy=${this.busy}
        @follow=${e => { const f = e.detail.flight; this.#follow(f.airport, f.direction, f.date, f.number); }}
        @unfollow=${e => this.#unfollow(e.detail.id)}></flight-details>`;
    }
    if (s?.kind === 'pass') {
      return html`<pass-legs .legs=${s.legs} ?canFollow=${this.canFollow} .bot=${this.bot}
        @follow-leg=${e => { const l = e.detail.leg; this.#follow(l.airport, l.direction, l.date, l.number); }}
        @show-leg=${e => this.#showLeg(e.detail.leg)}></pass-legs>`;
    }
    return '';
  }

  #updatedText() {
    if (!this.updatedAt) return '';
    const ago = Math.max(0, Math.round((this.now / 1000 - this.updatedAt) / 60));
    return ago < 1 ? 'Updated just now' : `Updated ${ago} min ago`;
  }

  #onSearch(e) {
    // Read now: once dispatch ends, an event from inside a shadow root has no target any more.
    const value = e.target.value;
    clearTimeout(this.searchTimer);
    this.searchTimer = setTimeout(() => { this.query = value; this.#saveUrl(); }, 120);
  }

  #onFile(e) {
    const file = e.target.files[0];
    e.target.value = '';
    if (file) this.#readPass(file);
  }

  #loadFull() {
    this.full = true;
    this.#toast('Loading two weeks, AENA takes about 15 seconds…', 15_000);
    this.#loadBoard();
  }
}

customElements.define('torre-app', TorreApp);
