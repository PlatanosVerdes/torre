import { LitElement, html, css } from '../../vendor/lit-3.3.3.js';
import { shared } from '../styles.js';
import { dayLabel, delay, place, statusText, tone } from '../format.js';
import { TelegramWebApp } from '../telegram.js';

// Everything about one flight, laid out like a departure board, with the follow button.
// Fires follow {flight}, unfollow {id} and sheet-close.
export class FlightDetails extends LitElement {
  static properties = {
    flight: { attribute: false },
    trackedId: { attribute: false },
    canFollow: { type: Boolean },
    notify: { type: Boolean },
    bot: { type: String },
    busy: { type: Boolean },
  };

  static styles = [shared, css`
    h3 { margin: 0 0 2px; font-size: 22px; display: flex; gap: 8px; align-items: center; }
    p { margin: 0 0 12px; color: var(--muted); }
    .board {
      background: var(--board); color: var(--board-ink); border-radius: 12px; padding: 14px;
      display: grid; grid-template-columns: 1fr 1fr; gap: 12px; font-variant-numeric: tabular-nums;
    }
    small { display: block; font-size: 11px; text-transform: uppercase; letter-spacing: .08em; opacity: .6; }
    b { font-size: 22px; font-family: ui-monospace, "SF Mono", Menlo, monospace; letter-spacing: .02em; }
    .wide { grid-column: 1 / -1; }
    .warn { color: var(--warn); margin-top: -4px; font-size: 14px; }
    .warn a { color: var(--accent); font-weight: 600; }
  `];

  render() {
    const f = this.flight;
    if (!f) return null;
    const arr = f.direction === 'L';
    const cells = [
      [arr ? 'Expected arrival' : 'Expected departure', f.estimated || f.scheduled],
      ['Delay', f.delayMin > 0 ? delay(f.delayMin) : '—'],
      ['Terminal', f.terminal || '—'],
      [arr ? 'Belt' : 'Gate', (arr ? f.belt : f.gate || f.zone) || '—'],
    ];
    if (!arr) cells.push(['Check-in', f.counters || '—']);
    cells.push(['Scheduled', f.scheduled + (f.estimatedDate ? ` · now ${f.estimatedDate.slice(0, 5)}` : '')]);
    const route = arr ? `${place(f)} → ${f.airport}` : `${f.airport} → ${place(f)}`;

    return html`
      <h3>${f.number} <span class="badge ${tone(f)}">${statusText(f)}</span></h3>
      <p>
        ${route} · ${f.airline} · ${dayLabel(f.date)}${f.aircraft ? ` · ${f.aircraft}` : ''}
        ${f.codeshare?.length ? html`<br>Also sold as ${f.codeshare.join(', ')}` : ''}
      </p>
      ${f.doubtful ? html`<p class="warn">* AENA lists it as departed, but its new time has not come yet, so Torre goes by the time.
        <a href="https://www.google.com/search?q=${encodeURIComponent(f.number + ' flight status')}" target="_blank" rel="noopener noreferrer">Check ${f.number} elsewhere</a></p>` : ''}
      <div class="board">
        ${cells.map((c, i) => html`
          <div class=${i === cells.length - 1 && cells.length % 2 ? 'wide' : ''}><small>${c[0]}</small><b>${c[1]}</b></div>`)}
      </div>
      ${this.#action(f)}
      <button class="btn secondary" @click=${() => this.#emit('sheet-close')}>Close</button>`;
  }

  #action(f) {
    if (this.trackedId) {
      return html`<button class="btn secondary" @click=${() => this.#emit('unfollow', { id: this.trackedId })}>Stop following</button>`;
    }
    if (this.canFollow) {
      return html`
        <button class="btn" ?disabled=${this.busy} @click=${() => this.#emit('follow', { flight: f })}>
          ${this.notify ? 'Follow and notify me' : 'Follow this flight'}
        </button>
        ${this.notify ? html`<div class="note">You get a Telegram message when its time, gate or status changes, until it departs or lands.</div>` : ''}`;
    }
    return followInTelegram(this.bot, f);
  }

  #emit(name, detail) {
    this.dispatchEvent(new CustomEvent(name, { detail, bubbles: true, composed: true }));
  }
}

export function followInTelegram(bot, flight) {
  return bot
    ? html`<a class="btn" href="https://t.me/${bot}?start=${TelegramWebApp.startParamFor(flight)}" target="_blank" rel="noopener">Follow in Telegram</a>`
    : html`<div class="note">Open Torre from Telegram to follow flights.</div>`;
}

customElements.define('flight-details', FlightDetails);
