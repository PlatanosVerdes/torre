// Telegram opens a Mini App with its launch data in the URL fragment. Reading it from there
// avoids loading telegram.org's script, so the page still loads nothing from other origins.
// See https://core.telegram.org/bots/webapps#initializing-mini-apps

const THEME = {
  bg_color: '--bg',
  secondary_bg_color: '--card',
  text_color: '--ink',
  hint_color: '--muted',
  link_color: '--accent',
  section_separator_color: '--line',
};

export class TelegramWebApp {
  constructor(hash = location.hash) {
    const p = new URLSearchParams(hash.slice(1));
    this.initData = p.get('tgWebAppData') || '';
    this.startParam = p.get('tgWebAppStartParam') || '';
    try { this.theme = JSON.parse(p.get('tgWebAppThemeParams') || '{}'); } catch { this.theme = {}; }
  }

  get inside() { return this.initData !== ''; }

  start() {
    if (!this.inside) return;
    const root = document.documentElement.style;
    for (const [key, token] of Object.entries(THEME)) {
      if (this.theme[key]) root.setProperty(token, this.theme[key]);
    }
    if (this.theme.bg_color) root.setProperty('--soft', this.theme.bg_color);
    this.post('web_app_ready');
    this.post('web_app_expand');
  }

  // A start parameter only allows [A-Za-z0-9_-], so a flight travels as BCN_S_18102026_FR2495.
  static startParamFor(f) {
    return f ? `${f.airport}_${f.direction}_${f.date.replaceAll('/', '')}_${f.number}` : '';
  }

  openTarget() {
    const m = /^([A-Z]{3})_([SL])_(\d{2})(\d{2})(\d{4})_([A-Z0-9]{3,8})$/.exec(this.startParam);
    return m ? { airport: m[1], dir: m[2], flight: `${m[3]}/${m[4]}/${m[5]}-${m[6]}` } : null;
  }

  post(eventType, eventData = {}) {
    try {
      if (window.TelegramWebviewProxy) window.TelegramWebviewProxy.postEvent(eventType, JSON.stringify(eventData));
      else window.parent.postMessage(JSON.stringify({ eventType, eventData }), 'https://web.telegram.org');
    } catch {}
  }
}
