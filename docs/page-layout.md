# Layout of the page

```
internal/server/web/
  index.html, app.css        the shell and the colour tokens, nothing else
  js/main.js                 builds the app
  js/api.js                  Api: the JSON API above
  js/telegram.js             TelegramWebApp: launch data, theme, start parameter
  js/boarding-pass.js        BoardingPass: barcode and Wallet reading, BCBP parsing
  js/format.js, styles.js    how a flight reads and looks, shared by every component
  js/components/             torre-app, airport-picker (from and to), flight-row, flight-details, pass-legs, torre-sheet
  vendor/                    Lit and zxing-wasm, pinned by version in the file name, with licences
```

Every file the page loads lives under `/static/<version>/`, where the version is a hash of the
embedded files, and only `index.html` (never cached) names it. Telegram's in-app browser keeps
scripts even when told to revalidate, and ES modules are cached by URL, so with fixed URLs a release
could run a new page on old components; with a URL per version it cannot.

Lit and zxing-wasm are vendored rather than loaded from a CDN, so the page has no third-party
origins and the Content-Security-Policy can say `'self'`. Lit is one ES module bundled from npm with
esbuild (`export * from 'lit'` plus the `repeat` and `classMap` directives); to update it, bundle the
new version the same way and change the file name.
