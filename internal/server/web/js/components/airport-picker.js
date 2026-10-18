import { LitElement, html, css } from '../../vendor/lit-3.3.3.js';
import { shared } from '../styles.js';
import { norm } from '../format.js';

const RECENT_KEY = 'recentAirports';
const RECENT_MAX = 4;

function loadRecent() {
  try { return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]'); } catch { return []; }
}

function saveRecent(list) {
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(list)); } catch {}
}

// A search-as-you-type airport field. It matches the code, the name and the city without accents
// ("malaga" finds Málaga), puts recently used airports first, and fires airport-change {iata}.
export class AirportPicker extends LitElement {
  static properties = {
    airports: { attribute: false },
    value: { type: String },
    open: { state: true },
    query: { state: true },
    active: { state: true },
  };

  static styles = [shared, css`
    :host { display: block; position: relative; flex: 1; min-width: 0; }
    input {
      width: 100%; font: inherit; font-weight: 600; color: var(--ink); background: var(--card);
      border: 1px solid var(--line); border-radius: 10px; padding: 9px 10px;
      text-overflow: ellipsis;
    }
    input:focus { outline: 2px solid var(--accent); outline-offset: -1px; font-weight: 400; }
    ul {
      position: absolute; left: 0; right: 0; top: calc(100% + 4px); z-index: 30; margin: 0; padding: 4px;
      list-style: none; background: var(--card); border: 1px solid var(--line); border-radius: 12px;
      box-shadow: 0 8px 24px rgba(0, 0, 0, .18); max-height: min(60vh, 360px); overflow-y: auto;
    }
    li {
      display: flex; gap: 10px; align-items: baseline; padding: 9px 10px; border-radius: 8px; cursor: pointer;
    }
    li[aria-selected="true"] { background: var(--soft); }
    .code { font-weight: 700; font-variant-numeric: tabular-nums; min-width: 34px; }
    .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .city { color: var(--muted); font-size: 13px; margin-left: auto; white-space: nowrap; }
    .label { padding: 6px 10px 2px; font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); cursor: default; }
    .none { padding: 12px 10px; color: var(--muted); cursor: default; }
  `];

  constructor() {
    super();
    this.airports = [];
    this.open = false;
    this.query = '';
    this.active = 0;
  }

  render() {
    const current = this.airports.find(a => a.iata === this.value);
    const shown = this.open ? this.query : current ? `${current.iata} · ${current.name}` : this.value || '';
    const { items, recent } = this.#matches();
    return html`
      <input type="text" role="combobox" aria-label="Airport" aria-expanded=${this.open} aria-controls="list"
        autocomplete="off" autocapitalize="characters" spellcheck="false" enterkeyhint="go"
        placeholder="Airport, city or code" .value=${shown}
        @focus=${this.#onFocus} @input=${this.#onInput} @keydown=${this.#onKey} @blur=${this.#onBlur}>
      ${this.open ? html`
        <ul id="list" role="listbox">
          ${recent ? html`<li class="label" role="presentation">Recent</li>` : ''}
          ${items.length ? items.map((a, i) => html`
            ${recent && i === recent ? html`<li class="label" role="presentation">All airports</li>` : ''}
            <li role="option" aria-selected=${i === this.active}
              @pointerdown=${e => { e.preventDefault(); this.#choose(a); }}
              @pointerenter=${() => { this.active = i; }}>
              <span class="code">${a.iata}</span><span class="name">${a.name}</span>
              ${a.city && !norm(a.name).includes(norm(a.city)) ? html`<span class="city">${a.city}</span>` : ''}
            </li>`)
          : html`<li class="none" role="presentation">No AENA airport matches</li>`}
        </ul>` : ''}`;
  }

  // Ranked: exact code, then code prefix, then a word of the name or city starting with the query,
  // then anywhere in them. With no query, recent airports come first and the rest follow.
  #matches() {
    const q = norm(this.query).trim();
    if (!q) {
      const recent = loadRecent().map(c => this.airports.find(a => a.iata === c)).filter(Boolean);
      const rest = this.airports.filter(a => !recent.includes(a));
      return { items: [...recent, ...rest], recent: recent.length };
    }
    const scored = [];
    for (const a of this.airports) {
      const code = a.iata.toLowerCase();
      const text = norm(`${a.name} ${a.city}`);
      let score = -1;
      if (code === q) score = 0;
      else if (code.startsWith(q)) score = 1;
      else if (text.split(/[\s\-/()]+/).some(w => w.startsWith(q))) score = 2;
      else if (text.includes(q)) score = 3;
      if (score >= 0) scored.push([score, a]);
    }
    scored.sort((x, y) => x[0] - y[0] || x[1].name.localeCompare(y[1].name));
    return { items: scored.map(s => s[1]), recent: 0 };
  }

  #onFocus(e) {
    this.query = '';
    this.active = 0;
    this.open = true;
    // Select-all would fight the keyboard on iOS; an empty field is what the user wants to type into.
    e.target.value = '';
  }

  #onInput(e) {
    this.query = e.target.value;
    this.active = 0;
    this.open = true;
  }

  #onKey(e) {
    const { items } = this.#matches();
    if (e.key === 'ArrowDown') { e.preventDefault(); this.active = Math.min(this.active + 1, items.length - 1); this.#scrollActive(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); this.active = Math.max(this.active - 1, 0); this.#scrollActive(); }
    else if (e.key === 'Enter') { e.preventDefault(); if (items[this.active]) this.#choose(items[this.active]); }
    else if (e.key === 'Escape') { this.#close(); }
  }

  #onBlur() { this.#close(); }

  #close() {
    this.open = false;
    this.query = '';
    this.renderRoot.querySelector('input')?.blur();
  }

  #choose(a) {
    saveRecent([a.iata, ...loadRecent().filter(c => c !== a.iata)].slice(0, RECENT_MAX));
    this.#close();
    if (a.iata !== this.value) {
      this.dispatchEvent(new CustomEvent('airport-change', { detail: { iata: a.iata }, bubbles: true, composed: true }));
    }
  }

  async #scrollActive() {
    await this.updateComplete;
    this.renderRoot.querySelector('li[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }
}

customElements.define('airport-picker', AirportPicker);
