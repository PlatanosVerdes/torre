import { LitElement, html, css } from '../../vendor/lit-3.3.3.js';
import { shared } from '../styles.js';
import { dayLabel, farEnd, tone } from '../format.js';

// One line of the board. A tap fires flight-select with the flight, and the tracked id when the
// row stands for a followed flight.
export class FlightRow extends LitElement {
  static properties = {
    flight: { attribute: false },
    trackId: { attribute: false },
    withRoute: { type: Boolean },
  };

  static styles = [shared, css`
    :host { display: block; border-top: 1px solid var(--line); }
    :host(:first-child) { border-top: 0; }
    button {
      display: grid; grid-template-columns: 54px minmax(0, 1fr) auto; gap: 2px 10px; padding: 10px 12px;
      width: 100%; text-align: left; background: none; border: 0; color: inherit; font: inherit; cursor: pointer;
    }
    .done { opacity: .55; }
    .time { font-variant-numeric: tabular-nums; font-weight: 700; font-size: 16px; }
    .time s { display: block; font-weight: 400; font-size: 12px; color: var(--muted); }
    .time.late { color: var(--warn); }
    .dest { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .iata { color: var(--muted); font-weight: 700; margin-right: 4px; }
    .sub { grid-column: 2; color: var(--muted); font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .side { grid-row: 1 / span 2; grid-column: 3; display: flex; flex-direction: column; align-items: flex-end; gap: 4px; }
    .where { font-size: 12px; color: var(--muted); font-variant-numeric: tabular-nums; }
    .where b { color: var(--ink); font-size: 14px; }
  `];

  render() {
    const f = this.flight;
    if (!f) return null;
    const codes = f.codeshare?.length ? ` · ${f.codeshare.length > 2 ? '+' + f.codeshare.length : f.codeshare.join(', ')}` : '';
    return html`
      <button class=${f.final ? 'done' : ''} @click=${this.#select}>
        ${f.estimated
          ? html`<div class="time ${f.delayMin >= 15 ? 'late' : ''}">${f.estimated}<s>${f.scheduled}</s></div>`
          : html`<div class="time">${f.scheduled}</div>`}
        <div class="dest">
          ${this.withRoute ? `${f.airport} ${f.direction === 'L' ? '←' : '→'} ` : ''}<span class="iata">${f.other}</span>${farEnd(f)}
        </div>
        <div class="side">
          <span class="badge ${tone(f)}">${f.statusLabel}</span>
          <span class="where">${this.#where(f)}</span>
        </div>
        <div class="sub">${f.number} · ${f.airline}${codes}${this.withRoute ? ` · ${dayLabel(f.date)}` : ''}</div>
      </button>`;
  }

  #where(f) {
    if (f.direction === 'L' && f.belt) return html`Belt <b>${f.belt}</b>`;
    if (f.gate) return html`Gate <b>${f.gate}</b>`;
    if (f.terminal) return html`<b>${f.terminal}</b>`;
    return '';
  }

  #select() {
    this.dispatchEvent(new CustomEvent('flight-select', {
      detail: { flight: this.flight, trackId: this.trackId },
      bubbles: true,
      composed: true,
    }));
  }
}

customElements.define('flight-row', FlightRow);
