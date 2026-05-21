# zapfast — frontend

Static GitHub Pages site for the zapfast project.

## Estrutura

- `index.html` — landing page (hero, features, comparison, pricing)
- `login.html` — auth screen (admin token via localStorage)
- `app/index.html` — dashboard (lista instâncias via /admin/users)
- `app/instance.html` — detalhe + QR code
- `app/send.html` — formulário de envio de texto
- `app/docs.html` — API reference
- `assets/` — logo, favicon, styles.css
- `_config.yml` — Jekyll config (only excludes scraped/)

## Stack

- Tailwind CSS via CDN
- Alpine.js para reatividade
- Fontes Google Fonts (Inter)
- Zero build step — sirva os arquivos estáticos diretamente

## Backend esperado

Base URL: `https://zapfast.5-161-161-140.nip.io`

Endpoints usados:
- `GET /health`
- `GET/POST/DELETE /admin/users` — Authorization header
- `POST /session/connect`, `GET /session/qr`, `GET /session/status` — Token header (instance token)
- `POST /chat/send/text` — Token header

## Deploy

GitHub Pages no branch `main` apontando para a raiz.
