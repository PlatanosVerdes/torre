import { LitElement, html, css } from '../../vendor/lit-3.3.3.js';
import { shared } from '../styles.js';
import { close, norm } from '../format.js';

const RECENT_MAX = 4;
// Thousands of airports would freeze a phone if all were drawn; typing narrows them anyway.
const SHOWN = 60;

function loadRecent(key) {
  try { return JSON.parse(localStorage.getItem(key) || '[]'); } catch { return []; }
}

function saveRecent(key, list) {
  try { localStorage.setItem(key, JSON.stringify(list)); } catch {}
}

// A search-as-you-type place field, one per end of the route. It matches the code, the name and
// the city without accents and forgives typos, puts recent picks first, offers "Anywhere" when
// anyLabel is set, and fires place-change {iata} ('' for anywhere).
//
// options: [{iata, name, city, country, alias, aena}]. aena is false outside Spain; alias holds the
// Spanish name AENA uses ("LONDRES /GATWICK"), so both languages find it.
export class AirportPicker extends LitElement {
  static properties = {
    options: { attribute: false },
    value: { type: String },
    label: { type: String },
    anyLabel: { type: String },
    recentKey: { type: String },
    // locate adds a location button to the field; a tap fires locate-request.
    locate: { type: Boolean },
    busy: { type: Boolean },
    open: { state: true },
    query: { state: true },
    active: { state: true },
  };

  static styles = [shared, css`
    :host { display: block; position: relative; min-width: 0; }
    .box {
      display: flex; align-items: center; gap: 8px; background: var(--card); border: 1px solid var(--line);
      border-radius: 10px; padding: 0 10px;
    }
    .box:focus-within { outline: 2px solid var(--accent); outline-offset: -1px; }
    .label { color: var(--muted); font-size: 13px; font-weight: 600; min-width: 34px; }
    input {
      flex: 1; min-width: 0; border: 0; background: none; outline: none; font: inherit; font-weight: 600;
      color: var(--ink); padding: 9px 0; text-overflow: ellipsis;
    }
    input:focus { font-weight: 400; }
    .here {
      flex: none; width: 32px; height: 32px; margin-right: -6px; border: 0; border-radius: 8px; background: none;
      color: var(--accent); display: grid; place-items: center; cursor: pointer;
    }
    .here:hover { background: var(--soft); }
    .here[disabled] { opacity: .5; cursor: progress; }
    input::placeholder { color: var(--muted); font-weight: 400; }
    ul {
      position: absolute; left: 0; right: 0; top: calc(100% + 4px); z-index: 30; margin: 0; padding: 4px;
      list-style: none; background: var(--card); border: 1px solid var(--line); border-radius: 12px;
      box-shadow: 0 8px 24px rgba(0, 0, 0, .18); max-height: min(55vh, 340px); overflow-y: auto;
    }
    li { display: flex; gap: 10px; align-items: baseline; padding: 9px 10px; border-radius: 8px; cursor: pointer; }
    li[aria-selected="true"] { background: var(--soft); }
    .code { font-weight: 700; font-variant-numeric: tabular-nums; min-width: 34px; }
    .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .note { color: var(--muted); font-size: 12px; margin-left: auto; white-space: nowrap; }
    .heading { padding: 6px 10px 2px; font-size: 11px; text-transform: uppercase; letter-spacing: .06em; color: var(--muted); cursor: default; }
    .none { padding: 12px 10px; color: var(--muted); cursor: default; }
  `];

  constructor() {
    super();
    this.options = [];
    this.value = '';
    this.open = false;
    this.query = '';
    this.active = 0;
  }

  render() {
    const current = this.options.find(o => o.iata === this.value);
    const shown = this.open ? this.query : current ? `${current.iata} · ${current.name}` : this.value;
    const { items: all, recent } = this.#matches();
    const items = all.slice(0, SHOWN);
    return html`
      <div class="box">
        <span class="label">${this.label}</span>
        <input type="text" role="combobox" aria-label=${this.label} aria-expanded=${this.open} aria-controls="list"
          autocomplete="off" autocapitalize="words" spellcheck="false" enterkeyhint="go"
          placeholder=${this.anyLabel || 'City, airport or code'} .value=${shown}
          @focus=${this.#onFocus} @input=${this.#onInput} @keydown=${this.#onKey} @blur=${this.#close}>
        ${this.locate ? html`<button class="here" aria-label="Use my location" title="Use my location" ?disabled=${this.busy}
            @click=${() => this.dispatchEvent(new CustomEvent('locate-request', { bubbles: true, composed: true }))}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M3 11l18-8-8 18-2-8z"/>
          </svg>
        </button>` : ''}
      </div>
      ${this.open ? html`
        <ul id="list" role="listbox">
          ${items.length ? items.map((o, i) => html`
            ${recent && i === 0 ? html`<li class="heading" role="presentation">Recent</li>` : ''}
            ${recent && i === recent ? html`<li class="heading" role="presentation">All</li>` : ''}
            <li role="option" aria-selected=${i === this.active}
              @pointerdown=${e => { e.preventDefault(); this.#choose(o); }}
              @pointerenter=${() => { this.active = i; }}>
              ${o.iata === '' ? html`<span class="name">${this.anyLabel}</span>` : html`
                <span class="code">${o.iata}</span><span class="name">${o.name}</span>
                ${o.aena === false ? html`<span class="note">${o.country || ''}</span>` : ''}`}
            </li>`)
          : html`<li class="none" role="presentation">No airport matches</li>`}
        </ul>` : ''}`;
  }

  // Ranked: exact code, code prefix, a word starting with the query, the query anywhere, then a
  // typo away. With no query: "Anywhere", recent picks, Spanish airports, then the rest.
  #matches() {
    const q = norm(this.query).trim();
    if (!q) {
      const recent = loadRecent(this.recentKey).map(c => this.options.find(o => o.iata === c)).filter(Boolean);
      const rest = this.options.filter(o => !recent.includes(o))
        .sort((a, b) => (a.aena === false) - (b.aena === false) || a.name.localeCompare(b.name));
      const any = this.anyLabel && this.value ? [{ iata: '' }] : [];
      return { items: [...any, ...recent, ...rest], recent: recent.length ? recent.length + any.length : 0 };
    }
    const parts = q.split(/\s+/);
    const scored = [];
    for (const o of this.options) {
      const code = o.iata.toLowerCase();
      const name = norm(o.name);
      const text = norm(`${o.name} ${o.city || ''} ${o.alias || ''}`);
      const words = text.split(/[\s\-/()]+/).filter(Boolean);
      let score = -1;
      if (code === q) score = 0;
      else if (code.startsWith(q)) score = 1;
      else if (name.startsWith(q)) score = 2;
      else if (parts.every(p => words.some(w => w.startsWith(p)))) score = 3;
      else if (text.includes(q) || text.replace(/\s+/g, '').includes(q.replace(/\s+/g, ''))) score = 4;
      else if (words.some(w => close(q, w)) || close(q, text.replace(/\s+/g, ''))) score = 5;
      if (score < 0) continue;
      // A place the loaded board flies to (an alias means AENA listed it) jumps a tier: JFK before
      // New York Stewart from Barcelona. Codes typed outright still win, and the unserved rest of
      // the world comes after Spain.
      const reach = o.aena !== false ? 0 : o.alias ? -1.2 : 0.6;
      scored.push([score + reach, o]);
    }
    scored.sort((x, y) => x[0] - y[0] || x[1].name.localeCompare(y[1].name));
    return { items: scored.map(s => s[1]), recent: 0 };
  }

  #onFocus(e) {
    this.query = '';
    this.active = 0;
    this.open = true;
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
    else if (e.key === 'Escape') { this.renderRoot.querySelector('input')?.blur(); }
  }

  #close() {
    this.open = false;
    this.query = '';
  }

  #choose(o) {
    if (o.iata && this.recentKey) {
      saveRecent(this.recentKey, [o.iata, ...loadRecent(this.recentKey).filter(c => c !== o.iata)].slice(0, RECENT_MAX));
    }
    this.renderRoot.querySelector('input')?.blur();
    if (o.iata !== this.value) {
      this.dispatchEvent(new CustomEvent('place-change', { detail: { iata: o.iata }, bubbles: true, composed: true }));
    }
  }

  async #scrollActive() {
    await this.updateComplete;
    this.renderRoot.querySelector('li[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }
}

customElements.define('airport-picker', AirportPicker);
