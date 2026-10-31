// The server's JSON API. Inside Telegram every call carries the signed launch data, which is how
// the server knows whose followed flights to return.

export class Api {
  constructor(initData = '') {
    this.initData = initData;
  }

  airports() { return this.#call('api/airports'); }

  places() { return this.#call('api/places'); }

  board(airport, dir, full) {
    return this.#call(`api/board?airport=${encodeURIComponent(airport)}&dir=${dir}${full ? '&range=full' : ''}`);
  }

  locate(number) { return this.#call(`api/locate?number=${encodeURIComponent(number)}`); }

  tracked() { return this.#call('api/tracked'); }

  // scheduled tells apart two rows of one number on a date; a boarding pass has none.
  follow(airport, direction, date, number, scheduled = '') {
    return this.#call('api/tracked', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ airport, direction, date, number, scheduled }),
    });
  }

  unfollow(id) { return this.#call('api/tracked/' + encodeURIComponent(id), { method: 'DELETE' }); }

  async #call(path, opts = {}) {
    const headers = { ...(opts.headers || {}) };
    if (this.initData) headers['X-Telegram-Init-Data'] = this.initData;
    const res = await fetch(path, { ...opts, headers });
    const body = res.status === 204 ? null : await res.json().catch(() => null);
    if (!res.ok) throw new Error(body?.error || `Error ${res.status}`);
    return body;
  }
}
