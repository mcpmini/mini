# Vendored libraries

ES module builds copied from the npm tarballs (checked against the registry's sha512) and served as-is.

| File | Package | Source file | License |
|---|---|---|---|
| `preact.js` | preact 10.24.3 | `dist/preact.module.js` | MIT |
| `preact-hooks.js` | preact 10.24.3 | `hooks/dist/hooks.module.js` | MIT |
| `htm.js` | htm 3.1.1 | `dist/htm.module.js` | Apache-2.0 |

The CSP allows only same-origin scripts and the page has no import map, so `preact-hooks.js` imports `./preact.js` instead of `preact`. The `sourceMappingURL` comments are removed because the maps aren't vendored.
