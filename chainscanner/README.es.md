<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt="ChainScanner"></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Sigue direcciones en la cadena y recibe sus transacciones por WebSocket, gRPC o HTTP.</strong></p>
<p align="center"><a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.vi.md">Tiếng Việt</a> · <strong>Español</strong> · <a href="README.ru.md">Русский</a></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">AGPL-3.0</a> &nbsp;·&nbsp; 18 redes</p>
<p align="center"><a href="docs/guide/es.md#quick-start">Inicio rápido</a> &nbsp;·&nbsp; <a href="docs/guide/es.md#networks">Redes compatibles</a> &nbsp;·&nbsp; <a href="docs/api/es.md">Referencia de la API</a></p>

---

## Por qué creamos ChainScanner

Al desarrollar GM Wallet y GMPay nos encontrábamos con el mismo problema. La cartera necesitaba saber qué saldos actualizar y el servicio de pagos necesitaba detectar transacciones nuevas en sus direcciones de cobro. Si cada aplicación consultaba por separado cada dirección, ambas gastaban cuota de RPC para detectar la misma transacción.

Con ChainScanner, todas las suscripciones de una red comparten el escaneo de bloques. Cada servicio recibe las transacciones de las direcciones que sigue y confirma su procesamiento por separado. La cartera puede actualizar solo los saldos afectados y GMPay puede verificar los pedidos correspondientes. Al volver a leer los mismos cuatro bloques de la red principal de Ethereum, hicimos 11 llamadas RPC en cada prueba, tanto con una dirección como con 100 o con dos servicios suscritos a esas mismas 100 direcciones.

Cada aplicación gestiona su lógica de negocio, el estado de sus pedidos y los saldos de sus carteras. ChainScanner ofrece una infraestructura compartida para suscribirse a direcciones, escanear bloques y distribuir eventos.

> [!IMPORTANT]
> La consulta por hash da prioridad a los respaldos, excluye los nodos que entregaron la actividad observada y devuelve la primera transacción válida. [Comparar proveedores de respaldo](#fallback-providers)

## Casos de uso

- **Cobros en cadena.** La [integración de EPUSDT](https://github.com/GMWalletApp/epusdt/pull/127) observa las direcciones de cobro y verifica la transacción mediante la consulta estricta antes de acreditar el pedido.
- **Idea para Telegram.** Un bot puede guardar los eventos de una cartera personal y avisar de entradas y salidas con el hash.
- **Idea para tesorería.** Contabilidad y alertas pueden observar las mismas carteras con consumidores y confirmaciones independientes.

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

## Nodo de respaldo recomendado

Registre cada respaldo por separado. Los intervalos fallidos se reparten entre los respaldos aptos. [API](docs/api/es.md#fallback)

```sh
BASE=http://127.0.0.1:9081
ADMIN_TOKEN=$(jq -er '.admin_token' chainscanner.json)
curl -fsS -X POST "$BASE/v1/admin/nodes/fallback" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
```

<a id="fallback-providers"></a>

### Proveedores de respaldo

Datos consultados el 2026-11-21. La elección es tuya. ChainScanner no garantiza disponibilidad, exactitud ni precios. Los créditos, CU y RU dependen del método. Verifica redes, historial y protocolos.

| Web | Fundación o lanzamiento y actividad | Cuota gratuita |
| --- | --- | --- |
| [Ankr](https://www.ankr.com/) | [2017 · infraestructura de nodos distribuidos](https://www.ankr.com/ankr-whitepaper-2.0.pdf) | [200 M de créditos API/mes con cuenta Freemium](https://www.ankr.com/docs/rpc-service/charging-policy/) |
| [Infura](https://www.infura.io/) | [2016 · infraestructura Ethereum e IPFS de Consensys](https://consensys.io/blog/press-release-infura-a-consensys-company-launches-infura-for-improved-ethereum-infrastructure-support) | [3 M de créditos/día en Core según la documentación](https://docs.infura.io/get-started/pricing/) |
| [PublicNode](https://www.publicnode.com/) | [2018 · inicio de operaciones de Allnodes, su operador](https://www.allnodes.com/about/) | [RPC público compartido sin cuota mensual individual publicada](https://www.publicnode.com/) |
| [QuickNode](https://www.quicknode.com/) | [2017 · RPC multirred y flujos de datos](https://www.quicknode.com/about) | [10 M de créditos API en una prueba de 30 días](https://support.quicknode.com/articles/5387074982-1-month-free-trial) |
| [Alchemy](https://www.alchemy.com/) | [2017 · API para desarrolladores y acceso a nodos](https://www.alchemy.com/company) | [30 M CU/mes](https://www.alchemy.com/docs/reference/pricing-plans) |
| [BlockPI](https://blockpi.io/) | [2022-10-25 · lanzamiento alfa de su red RPC distribuida](https://docs.blockpi.io/welcome-to-blockpi/about-blockpi) | [50 M RU/mes con registro](https://docs.blockpi.io/pricing/pricing-and-rate-limit) |
| [dRPC](https://drpc.org/) | [2023-03 · acceso público a su pasarela RPC descentralizada](https://blog.drpc.org/how-drpcs-free-tier-works/) | [50 M CU cada 30 días](https://drpc.org/docs/howitworks/ratelimiting) |

## Gracias por usar ChainScanner

Si tu proyecto usa ChainScanner o se inspira en su diseño, te agradeceríamos que enlazaras el repositorio en tu README. Tu recomendación significa mucho para nuestro equipo y ayuda a otros desarrolladores a encontrarnos. ¡Nos alegra haberte ayudado!

```markdown
Proyecto de referencia: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```

Gracias a imToken y Bitget Wallet por sus RPC públicos y a ChainList, OpenChainBench y CompareNodes por sus directorios. Gracias a quienes usan ChainScanner y confían en él.
