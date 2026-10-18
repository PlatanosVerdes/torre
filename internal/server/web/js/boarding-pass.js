// Boarding passes are read on the phone and only the flight leaves it: the barcode also holds the
// passenger's name and booking reference, which together open the booking at the airline.

const READER = 'vendor/zxing-reader-3.1.5.js';
const WASM = 'vendor/zxing_reader-3.1.5.wasm';

export class BoardingPass {
  static #reader;

  // read returns the legs on a pass: [{from, to, number, date}].
  static async read(file) {
    if (/\.pdf$/i.test(file.name) || file.type === 'application/pdf') {
      throw new Error('PDFs are not supported yet. Take a screenshot of the barcode instead.');
    }
    const wallet = /\.pkpass$/i.test(file.name) || file.type === 'application/vnd.apple.pkpass';
    const text = wallet ? await BoardingPass.#fromWallet(file) : await BoardingPass.#fromImage(file);
    const legs = text ? BoardingPass.parse(text) : [];
    if (!legs.length) throw new Error('No boarding pass barcode found. Try a sharper photo or a screenshot.');
    return legs;
  }

  // parse reads IATA Resolution 792 (Bar Coded Boarding Pass): fixed-width fields per leg. Route,
  // carrier, flight and day are kept; name, booking reference and seat are skipped.
  static parse(text) {
    if (!/^M[1-4]/.test(text) || text.length < 60) return [];
    const legs = [];
    let p = 23;
    for (let i = 0; i < +text[1] && p + 37 <= text.length; i++) {
      const from = text.slice(p + 7, p + 10).trim();
      const to = text.slice(p + 10, p + 13).trim();
      const carrier = text.slice(p + 13, p + 16).trim();
      const flight = text.slice(p + 16, p + 21).trim().replace(/^0+(?=\d)/, '');
      const day = parseInt(text.slice(p + 21, p + 24), 10);
      const extra = parseInt(text.slice(p + 35, p + 37), 16) || 0;
      if (/^[A-Z]{3}$/.test(from) && /^[A-Z]{3}$/.test(to) && carrier && flight && day >= 1 && day <= 366) {
        legs.push({ from, to, number: carrier + flight, date: BoardingPass.dateOf(day) });
      }
      p += 37 + extra;
    }
    return legs;
  }

  // The pass carries the day of the year but no year, so the nearest one not in the past is taken.
  static dateOf(day, today = new Date()) {
    const madrid = today.toLocaleDateString('en-GB', { timeZone: 'Europe/Madrid' }).split('/').map(Number);
    const now = Date.UTC(madrid[2], madrid[1] - 1, madrid[0]);
    for (const year of [madrid[2], madrid[2] + 1]) {
      const d = new Date(Date.UTC(year, 0, day));
      if (d.getTime() >= now - 864e5) {
        return `${String(d.getUTCDate()).padStart(2, '0')}/${String(d.getUTCMonth() + 1).padStart(2, '0')}/${year}`;
      }
    }
    return null;
  }

  static async #fromImage(file) {
    const zx = await BoardingPass.#loadReader();
    const results = await zx.readBarcodes(file, {
      formats: ['Aztec', 'PDF417', 'QRCode', 'DataMatrix'],
      tryHarder: true,
      maxNumberOfSymbols: 4,
    });
    return results.find(r => r.isValid && /^M[1-4]/.test(r.text))?.text ?? null;
  }

  // A Wallet pass is a zip holding pass.json, whose barcode message is the BCBP text itself.
  static async #fromWallet(file) {
    const buf = new Uint8Array(await file.arrayBuffer());
    const dv = new DataView(buf.buffer);
    let end = -1;
    for (let i = buf.length - 22; i >= Math.max(0, buf.length - 65557); i--) {
      if (dv.getUint32(i, true) === 0x06054b50) { end = i; break; }
    }
    if (end < 0) throw new Error('Not a Wallet pass');

    let p = dv.getUint32(end + 16, true);
    for (let n = dv.getUint16(end + 10, true); n > 0 && dv.getUint32(p, true) === 0x02014b50; n--) {
      const method = dv.getUint16(p + 10, true);
      const size = dv.getUint32(p + 20, true);
      const nameLen = dv.getUint16(p + 28, true);
      const skip = nameLen + dv.getUint16(p + 30, true) + dv.getUint16(p + 32, true);
      const local = dv.getUint32(p + 42, true);
      if (new TextDecoder().decode(buf.subarray(p + 46, p + 46 + nameLen)) === 'pass.json') {
        const start = local + 30 + dv.getUint16(local + 26, true) + dv.getUint16(local + 28, true);
        let data = buf.subarray(start, start + size);
        if (method === 8) {
          const stream = new Blob([data]).stream().pipeThrough(new DecompressionStream('deflate-raw'));
          data = new Uint8Array(await new Response(stream).arrayBuffer());
        }
        const pass = JSON.parse(new TextDecoder().decode(data));
        return pass.barcodes?.[0]?.message || pass.barcode?.message || null;
      }
      p += 46 + skip;
    }
    throw new Error('The pass has no pass.json');
  }

  // The reader is ~1 MB, so it loads on the first pass rather than with the page.
  static #loadReader() {
    BoardingPass.#reader ??= new Promise((resolve, reject) => {
      const s = document.createElement('script');
      s.src = READER;
      s.onload = () => {
        window.ZXingWASM.prepareZXingModule({
          overrides: { locateFile: (path, prefix) => path.endsWith('.wasm') ? WASM : prefix + path },
        });
        resolve(window.ZXingWASM);
      };
      s.onerror = () => {
        BoardingPass.#reader = null;
        reject(new Error('Could not load the barcode reader'));
      };
      document.head.appendChild(s);
    });
    return BoardingPass.#reader;
  }
}
