# Boarding passes

"Add boarding pass" takes a photo or screenshot of the barcode, or an Apple Wallet `.pkpass`.

The pass is decoded **on the phone** and only the flight is sent to the server. A boarding pass
barcode (IATA BCBP, Resolution 792) also holds the passenger's name and the booking reference, and
those two together open the booking at the airline, so neither leaves the page.

- **Images** are read with [zxing-wasm](https://github.com/Sec-ant/zxing-wasm) (zxing-cpp compiled to
  WebAssembly): Aztec, PDF417, QR and Data Matrix, which covers every airline's pass. It is ~1 MB, so
  it loads on the first pass rather than with the page, and it is served from the Pi.
- **Wallet passes** are a zip; the BCBP text is `pass.json`'s barcode message, unzipped in the page
  with `DecompressionStream`. No image decoding involved.
- **PDFs are not read.** Airlines draw the barcode in too many ways to turn a PDF into an image
  reliably in the browser; a screenshot of it works.

Each leg is matched to the AENA board that has it: departures at the origin when AENA runs it, else
arrivals at the destination. The pass carries the day of the year but not the year, so the nearest
date that is not in the past is taken.
