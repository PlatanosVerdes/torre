import { LitElement, html, css } from '../../vendor/lit-3.3.3.js';
import { shared } from '../styles.js';

// The panel that slides up from the bottom. Whatever goes in the slot is its content; a tap on
// the backdrop fires sheet-close.
export class TorreSheet extends LitElement {
  static properties = { open: { type: Boolean, reflect: true } };

  static styles = [shared, css`
    .bg { position: fixed; inset: 0; background: rgba(0, 0, 0, .45); z-index: 10; display: none; }
    .panel {
      position: fixed; left: 0; right: 0; bottom: 0; z-index: 11; background: var(--card); color: var(--ink);
      border-radius: 16px 16px 0 0; padding: 18px 16px calc(18px + env(safe-area-inset-bottom));
      transform: translateY(100%); transition: transform .2s ease; max-width: 640px; margin: 0 auto;
      max-height: 92vh; overflow-y: auto;
    }
    :host([open]) .bg { display: block; }
    :host([open]) .panel { transform: none; }
  `];

  render() {
    return html`
      <div class="bg" @click=${() => this.dispatchEvent(new CustomEvent('sheet-close', { bubbles: true, composed: true }))}></div>
      <div class="panel" role="dialog" aria-modal="true"><slot></slot></div>`;
  }
}

customElements.define('torre-sheet', TorreSheet);
