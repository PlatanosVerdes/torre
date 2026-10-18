import { css } from '../vendor/lit-3.3.3.js';

// The pieces every component draws the same way. Colours come from the custom properties in
// app.css, which reach inside each shadow root and which Telegram's theme overrides.
export const shared = css`
  :host { box-sizing: border-box; }
  *, *::before, *::after { box-sizing: inherit; }

  .badge {
    font-size: 12px; font-weight: 600; padding: 2px 8px; border-radius: 999px;
    background: var(--soft); color: var(--muted); white-space: nowrap;
  }
  .badge.ok { color: var(--ok); }
  .badge.warn { color: var(--warn); }
  .badge.bad { color: var(--bad); }
  .badge.go { background: var(--accent); color: #fff; }

  .btn {
    display: block; width: 100%; margin-top: 12px; padding: 13px; border: 0; border-radius: 12px;
    font: inherit; font-weight: 700; background: var(--accent); color: #fff; text-align: center;
    text-decoration: none; cursor: pointer;
  }
  .btn.secondary { background: var(--soft); color: var(--ink); }
  .btn:disabled { opacity: .6; }

  .note { font-size: 12px; color: var(--muted); margin-top: 8px; text-align: center; }
  .muted { color: var(--muted); }
`;
