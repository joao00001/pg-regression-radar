# Vendored third-party scripts

| File | Version | Source | SHA-256 |
|---|---|---|---|
| `mermaid.min.js` | 11.17.2 | `npm pack mermaid@11.17.2`, file `dist/mermaid.min.js` (MIT, see `mermaid.LICENSE`) | `581ed7d74bd9048d0e3a91363927d72ef22942d7722546b27f7cc29e35390eb8` |

The docs render diagrams with Mermaid. Material for MkDocs would otherwise fetch
it at runtime from `unpkg.com/mermaid@11` (an unpinned third-party URL); a
global `mermaid` makes it use this copy instead. `overrides/main.html` loads it
only on pages that contain a diagram. To upgrade: `npm pack mermaid@<version>`,
replace the file, update the version and hash above.
