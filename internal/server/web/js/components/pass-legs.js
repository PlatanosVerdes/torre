import { LitElement, html, css } from '../../vendor/lit-3.3.3.js';
import { shared } from '../styles.js';
import { dayLabel } from '../format.js';
import { followInTelegram } from './flight-details.js';

// The flights found on a boarding pass. Each leg is matched to the AENA board that has it: the
// departure airport when AENA runs it, else the arrival one. Fires follow-leg, show-leg and sheet-close.
export class PassLegs extends LitElement {
  static properties = {
    legs: { attribute: false },
    canFollow: { type: Boolean },
    bot: { type: String },
  };

  static styles = [shared, css`
    h3 { margin: 0 0 2px; font-size: 22px; }
    p { margin: 0 0 12px; color: var(--muted); }
    .legs { display: grid; gap: 8px; }
    .leg { background: var(--soft); border-radius: 12px; padding: 12px; }
    .leg b { font-size: 17px; }
    .leg p { margin: 2px 0 0; }
    .leg .btn { margin-top: 8px; }
  `];

  render() {
    return html`
      <h3>Boarding pass</h3>
      <p>Only the flight is used. Your name and booking reference stay on this phone.</p>
      <div class="legs">${(this.legs || []).map(l => this.#leg(l))}</div>
      ${this.canFollow ? '' : followInTelegram(this.bot, this.legs?.find(l => l.airport) && this.#asFlight(this.legs.find(l => l.airport)))}
      <button class="btn secondary" @click=${() => this.#emit('sheet-close')}>Close</button>`;
  }

  #leg(l) {
    const usable = l.airport && l.date;
    return html`
      <div class="leg">
        <b>${l.number}</b> ${l.from} → ${l.to}
        <p>${dayLabel(l.date)}${l.airport ? '' : ' · neither airport is run by AENA'}</p>
        ${usable && this.canFollow ? html`<button class="btn" @click=${() => this.#emit('follow-leg', { leg: l })}>Follow</button>` : ''}
        ${usable && !this.canFollow ? html`<button class="btn secondary" @click=${() => this.#emit('show-leg', { leg: l })}>Show on the board</button>` : ''}
      </div>`;
  }

  #asFlight(l) {
    return { airport: l.airport, direction: l.direction, date: l.date, number: l.number };
  }

  #emit(name, detail) {
    this.dispatchEvent(new CustomEvent(name, { detail, bubbles: true, composed: true }));
  }
}

customElements.define('pass-legs', PassLegs);
