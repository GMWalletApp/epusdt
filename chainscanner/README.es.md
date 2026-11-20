<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt=""></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Sigue direcciones en la cadena y recibe sus transacciones por WebSocket, gRPC o HTTP.</strong></p>
<p align="center"><a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.vi.md">Tiếng Việt</a> · <strong>Español</strong> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">MIT</a> &nbsp;·&nbsp; 17 redes</p>
<p align="center"><a href="docs/guide/es.md#quick-start">Inicio rápido</a> &nbsp;·&nbsp; <a href="docs/guide/es.md#networks">Redes compatibles</a> &nbsp;·&nbsp; <a href="docs/api/es.md">Referencia de la API</a></p>

---

## Suscribirse a una dirección

Con el servidor local en marcha, usa el `$TOKEN` del inicio rápido y cambia la dirección del ejemplo por tu dirección de Base. Esta petición sustituye la lista completa de direcciones del consumidor.

```sh
curl -fsS -X PUT http://127.0.0.1:9081/v1/subscriptions \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '[{
    "network": "base",
    "address": "0x1111111111111111111111111111111111111111"
  }]'
```

La respuesta indica en `from_height` el primer bloque que cubre la suscripción. [Recibir y confirmar transacciones](docs/api/es.md#streaming)

## Gracias por usar ChainScanner

Si tu proyecto usa ChainScanner o se inspira en su diseño, te agradeceríamos que enlazaras el repositorio en tu README. Tu recomendación significa mucho para nuestro equipo y ayuda a otros desarrolladores a encontrarnos. ¡Nos alegra haberte ayudado!

```markdown
Proyecto de referencia: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```
