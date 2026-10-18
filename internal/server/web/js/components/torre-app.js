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
const iata = (s) => /^[A-Z]{3}$/.test((s || '').toUpperCase()) ? s.toUpperCase() : '';

// A long board renders this many rows; the search narrows it far below that.
const MAX_ROWS = 400;

// The whole page: the route (from, to), day chips, followed flights, the board and the sheet.
//
// AENA only has boards for Spanish airports, so the route decides which one to read: departures
// at `from` when it is an AENA airport (narrowed to `to` if set), otherwise arrivals at `to`
// (narrowed to `from`). A route with neither end in Spain has no board.
export class TorreApp extends LitElement {
  static properties = {
    from: { state: true },
    to: { state: true },
    query: { state: true },
    day: { state: true },
    showDeparted: { state: true },
    full: { state: true },
    flights: { state: true },
    loaded: { state: true },
    updatedAt: { state: true },
    error: { state: true },
    airports: { state: true },
    world: { state: true },
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
    .top { display: flex; gap: 8px; align-items: center; justify-content: space-between; margin-bottom: 8px; }
    h1 { font-size: 20px; margin: 0; letter-spacing: -0.02em; }
    .route { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 6px 8px; align-items: center; }
    .route airport-picker:nth-of-type(1) { grid-row: 1; grid-column: 1; }
    .route airport-picker:nth-of-type(2) { grid-row: 2; grid-column: 1; }
    .swap {
      grid-row: 1 / span 2; grid-column: 2; width: 40px; height: 40px; border-radius: 999px; border: 1px solid var(--line);
      background: var(--card); color: var(--ink); font: inherit; font-size: 18px; cursor: pointer;
    }
    input[type=search], .tool {
      font: inherit; color: var(--ink); background: var(--card); border: 1px solid var(--line); border-radius: 10px;
    }
    input[type=search] { width: 100%; margin-top: 8px; font-size: 16px; padding: 9px 12px; }
    .tool { font-size: 13px; font-weight: 600; padding: 6px 10px; cursor: pointer; }
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
    // a/d are the old single-airport links (and what the bot's buttons carry): a departures
    // board is "from" that airport, an arrivals board "to" it.
    const legacy = target ? { a: target.airport, d: target.dir } : { a: params.get('a'), d: (params.get('d') || 'S').toUpperCase() };
    if (iata(legacy.a)) {
      this.from = legacy.d === 'L' ? '' : iata(legacy.a);
      this.to = legacy.d === 'L' ? iata(legacy.a) : '';
    } else if (params.has('from') || params.has('to')) {
      // A link names the whole route; mixing in the last visit's other end would change it.
      this.from = iata(params.get('from'));
      this.to = iata(params.get('to'));
    } else {
      this.from = iata(store.get('from', 'BCN'));
      this.to = iata(store.get('to', ''));
    }
    this.query = params.get('q') || '';
    this.pendingOpen = target?.flight || params.get('f') || '';
    this.day = null;
    this.showDeparted = false;
    this.full = false;
    this.flights = [];
    this.loaded = '';
    this.airports = [];
    this.world = [];
    this.tracked = [];
    this.canFollow = false;
    this.sheet = null;
    this.now = Date.now();
    this.loading = 0;
  }

  connectedCallback() {
    super.connectedCallback();
    this.#saveUrl();
    this.#loadAirports().then(() => this.#loadBoard()).then(() => this.#loadWorld());
    this.#loadTracked();
    this.onVisible = () => document.visibilityState === 'visible' && this.#refresh();
    document.addEventListener('visibilitychange', this.onVisible);
    this.timers = [setInterval(() => this.onVisible(), 60_000), setInterval(() => { this.now = Date.now(); }, 30_000)];
  }

  disconnectedCallback() {
    super.disconnectedCallback();
    document.removeEventListener('visibilitychange', this.onVisible);
    this.timers.forEach(clearInterval);
  }

  // --- Route ----------------------------------------------------------------------------------

  #isAena(code) { return !!code && this.airports.some(a => a.iata === code); }

  // route is the board the current from/to reads, and which flights on it belong to the route.
  #route() {
    if (this.#isAena(this.from)) return { airport: this.from, dir: 'S', other: this.to };
    if (this.#isAena(this.to)) return { airport: this.to, dir: 'L', other: this.from };
    return null;
  }

  #setRoute(from, to) {
    this.from = from;
    this.to = to;
    this.#saveUrl();
    const r = this.#route();
    if (!r) { this.flights = []; this.loaded = ''; return; }
    if (`${r.airport}/${r.dir}` !== this.loaded) {
      this.day = null; this.full = false; this.flights = []; this.error = '';
      this.#loadBoard();
    }
  }

  // places offers AENA's airports first, then every other airport with scheduled flights. AENA's
  // own names are kept for its airports, and its Spanish names for the rest stay searchable.
  #places() {
    const c = this.placesCache;
    if (c?.airports === this.airports && c?.world === this.world && c?.flights === this.flights) return c.list;
    const alias = new Map(this.flights.map(f => [f.other, f.otherLocal]));
    const known = new Set(this.airports.map(a => a.iata));
    const list = this.airports.map(a => ({ iata: a.iata, name: a.name, city: a.city, aena: true }));
    for (const p of this.world) {
      if (!known.has(p.iata)) list.push({ ...p, alias: alias.get(p.iata) || '', aena: false });
    }
    this.placesCache = { airports: this.airports, world: this.world, flights: this.flights, list };
    return list;
  }

  // --- Data -----------------------------------------------------------------------------------

  async #loadAirports() {
    try { this.airports = await this.api.airports(); } catch {}
  }

  // The world's airports only feed the pickers, so they load after the board, not before it.
  async #loadWorld() {
    try { this.world = await this.api.places(); } catch {}
  }

  async #loadBoard() {
    const r = this.#route();
    if (!r) return;
    const my = ++this.loading;
    try {
      const data = await this.api.board(r.airport, r.dir, this.full);
      if (my !== this.loading) return;
      this.flights = data.flights;
      this.loaded = `${r.airport}/${r.dir}`;
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

  // visible is the board narrowed to the route, the day and the search. When the search matches
  // nothing exactly it retries tolerating typos, and fuzzy says so, so the page can tell the user.
  #visible() {
    const other = this.#route()?.other;
    const compact = norm(this.query).replace(/\s+/g, '');
    const words = norm(this.query).split(/\s+/).filter(Boolean);
    const inRoute = this.flights.filter(f =>
      (!other || f.other === other) &&
      (!this.day || f.date === this.day) &&
      (this.showDeparted || compact || !f.final));
    if (!words.length) return { flights: inRoute, fuzzy: false };

    const fields = (f) => [f.number, ...(f.codeshare || []), f.airline, f.otherCity, f.otherLocal, f.other].map(norm);
    const exact = inRoute.filter(f => {
      const h = fields(f).join(' ');
      return h.replace(/\s+/g, '').includes(compact) || words.every(w => h.includes(w));
    });
    if (exact.length) return { flights: exact, fuzzy: false };

    // Each field also counts written as one word, so "aireropa" can reach "Air Europa".
    const tokens = (f) => fields(f).flatMap(x => [...x.split(/[\s\-/()]+/), x.replace(/[\s\-/()]+/g, '')]).filter(Boolean);
    const near = inRoute.filter(f => { const t = tokens(f); return words.every(w => t.some(x => x.includes(w) || close(w, x))); });
    return { flights: near, fuzzy: near.length > 0 };
  }

  #saveUrl() {
    store.set('from', this.from);
    store.set('to', this.to);
    const p = new URLSearchParams();
    if (this.from) p.set('from', this.from);
    if (this.to) p.set('to', this.to);
    if (this.query) p.set('q', this.query);
    // The fragment holds Telegram's launch data; dropping it would sign the user out.
    history.replaceState(null, '', '?' + p + location.hash);
  }

  // --- Actions --------------------------------------------------------------------------------

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
      this.message = '';
      this.sheet = {
        kind: 'pass',
        legs: legs.map(l => this.#isAena(l.from) ? { ...l, airport: l.from, direction: 'S' }
          : this.#isAena(l.to) ? { ...l, airport: l.to, direction: 'L' }
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
    this.#setRoute(l.from, l.to);
    // Same board as before: setRoute kept the flights and loads nothing, so open the leg from here.
    if (this.flights.length) this.#loadBoard();
  }

  #toast(text, ms = 3200) {
    this.message = text;
    clearTimeout(this.toastTimer);
    this.toastTimer = setTimeout(() => { this.message = ''; }, ms);
  }

  // --- Rendering ------------------------------------------------------------------------------

  render() {
    const route = this.#route();
    const { flights: visible, fuzzy } = this.#visible();
    const places = this.#places();
    return html`
      <header>
        <div class="top">
          <h1>Torre</h1>
          <button class="tool" @click=${() => this.renderRoot.querySelector('input[type=file]').click()}>Add boarding pass</button>
          <input type="file" accept="image/*,.pkpass,application/vnd.apple.pkpass" @change=${this.#onFile}>
        </div>
        <div class="route">
          <airport-picker label="From" anyLabel="Anywhere" recentKey="recentFrom" .options=${places} .value=${this.from}
            @place-change=${e => this.#setRoute(e.detail.iata, this.to)}></airport-picker>
          <airport-picker label="To" anyLabel="Anywhere" recentKey="recentTo" .options=${places} .value=${this.to}
            @place-change=${e => this.#setRoute(this.from, e.detail.iata)}></airport-picker>
          <button class="swap" aria-label="Swap from and to" @click=${() => this.#setRoute(this.to, this.from)}>⇅</button>
        </div>
        <input type="search" .value=${this.query} placeholder="Flight or airline (FR2495, Vueling)"
          autocomplete="off" enterkeyhint="search" @input=${this.#onSearch}>
        ${route ? html`<div class="chips">
          ${this.#days().map(d => html`<button class="chip" aria-pressed=${d === this.day} @click=${() => { this.day = d; }}>${dayLabel(d)}</button>`)}
          <button class="chip" aria-pressed=${this.showDeparted} @click=${() => { this.showDeparted = !this.showDeparted; }}>Show departed</button>
        </div>` : ''}
      </header>
      ${route ? html`
        <div class="meta"><span>${this.#updatedText()}</span><span>${this.flights.length ? `${visible.length} ${visible.length === 1 ? 'flight' : 'flights'}` : ''}</span></div>
        ${fuzzy ? html`<div class="meta">No exact match for “${this.query}”, showing close ones</div>` : ''}` : ''}
      <main>
        ${this.tracked.length ? html`<h2>Following</h2><div class="list">${this.tracked.map(t => this.#trackedRow(t))}</div>` : ''}
        <h2>${this.#heading(route)}</h2>
        <div class="list" @flight-select=${e => this.#openFlight(e.detail.flight, e.detail.trackId)}>${this.#board(route, visible)}</div>
        ${route && !this.full ? html`<button class="more" @click=${this.#loadFull}>Show more days</button>` : ''}
      </main>
      <torre-sheet ?open=${!!this.sheet} @sheet-close=${() => { this.sheet = null; }}>${this.#sheetContent()}</torre-sheet>
      ${this.message ? html`<div class="toast" role="status">${this.message}</div>` : ''}`;
  }

  #heading(route) {
    if (!route) return 'Flights';
    if (this.from && this.to) return `${this.from} → ${this.to}`;
    return route.dir === 'S' ? `Departures · ${route.airport}` : `Arrivals · ${route.airport}`;
  }

  #board(route, visible) {
    if (!route) {
      if (!this.airports.length && (this.from || this.to)) return html`<div class="empty">Loading…</div>`;
      return html`<div class="empty">${this.from || this.to
        ? 'Torre has flights from or to Spanish airports only (AENA). Make one end of the route a Spanish airport.'
        : 'Choose where you fly from, where you fly to, or both.'}</div>`;
    }
    if (this.error) return html`<div class="error">${this.error}</div>`;
    if (!this.flights.length) return html`<div class="empty">Loading…</div>`;
    if (!visible.length) {
      return html`<div class="empty">${this.query ? 'No flight matches' : 'No flights on this route that day'}${this.full ? '' : '. Try “Show more days”.'}</div>`;
    }
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
