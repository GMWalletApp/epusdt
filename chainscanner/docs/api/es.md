# ChainScanner · Referencia de la API

[English](en.md) · [简体中文](zh-CN.md) · [Tiếng Việt](vi.md) · **Español** · [Русский](ru.md)

[Inicio](../../README.es.md) · [Inicio rápido](../guide/es.md#quick-start) · [Conexión y autenticación](#auth)  ·  [Reemplazar suscripciones](#subscriptions)  ·  [Leer y confirmar entregas](#deliveries)  ·  [Suscripciones en tiempo real](#streaming)  ·  [Administración de nodos](#nodes)  ·  [Manejo de errores](#errors)

Usa el token del consumidor para suscribirte, leer y confirmar entregas. La gestión de nodos requiere otro token de administración. Los ejemplos usan el servidor local de la guía de inicio.

<a id="auth"></a>

## Conexión y autenticación

Incluye `Authorization: Bearer <token>` en cada petición. El token identifica al consumidor y su cola. Todos los tokens deben ser distintos y tener al menos 24 bytes. Configura `admin_token` para habilitar las rutas administrativas. Envía JSON con `Content-Type: application/json`. Las respuestas correctas con cuerpo son JSON; HTTP 204 no lleva cuerpo. Los errores suelen ser texto plano.

```sh
BASE=http://127.0.0.1:9081
TOKEN=$(jq -r '.consumers.payments' chainscanner.json)
ADMIN=$(jq -r '.admin_token' chainscanner.json)
consumer() { curl -sS -i -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
admin() { curl -sS -i -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' "$@"; }
```

<a id="routes"></a>

## Índice de operaciones

| Rol | HTTP | Éxito |
| --- | --- | --- |
| Consumidor | [`PUT /v1/subscriptions`](#replace-watches) | `200 / 202` |
| Consumidor | [`GET /v1/events/next`](#next) | `200 / 204` |
| Consumidor | [`GET /v1/events/stream`](#streaming) · WebSocket | `101` |
| Consumidor | [`POST /v1/events/{id}/ack`](#ack) | `204` |
| Consumidor | [`GET /v1/heights/{network}`](#height) | `200` |
| Consumidor | [`GET /v1/transactions/{network}/{hash}`](#transactions) | `200` |
| Administrador | [`GET /v1/admin/progress`](#scan-progress) | `200` |
| Administrador | [`GET /v1/admin/scan-retry-rate`](#scan-retry-rate) | `200`, `400` |
| Administrador | [`GET /v1/admin/nodes`](#list) | `200` |
| Administrador | [`POST /v1/admin/nodes`](#add) | `201` |
| Administrador | [`POST /v1/admin/nodes/fallback`](#fallback) | `201` |
| Administrador | [`POST /v1/admin/nodes/{id}/test`](#test-one) | `200` |
| Administrador | [`POST /v1/admin/nodes/test`](#test-all) | `200` |
| Administrador | [`DELETE /v1/admin/nodes/{id}`](#delete) | `204` |
| Administrador | [`GET /v1/admin/blacklist`](#blacklist) | `200` |
| Administrador | [`PUT /v1/admin/blacklist/{host}`](#blacklist) | `204` |
| Administrador | [`DELETE /v1/admin/blacklist/{host}`](#blacklist) | `204` |

<a id="transactions"></a>

## Consultar una transacción

Usa el token del consumidor y una red configurada. No se necesita una suscripción. La respuesta incluye `network`, `hash` y `result`.

La consulta por hash da prioridad a los respaldos, excluye los nodos que entregaron la actividad observada y devuelve la primera transacción válida.

HTTP 404 con `X-ChainScanner-Evidence: not_found` indica que el nodo no encontró la transacción. El pedido puede pasar a revisión manual. Ante 502 o 503, vuelva a intentar la consulta tras una pausa.

Tras un fallo, se prueban otros nodos. Si hay alternativas y queda tiempo, se reintenta al menos tres veces. Use `?fallback_only=true` para restringir la consulta a respaldos. Con `?observed=true` se requiere un origen de escaneo registrado. Si falta el origen o no hay un nodo independiente, se devuelve 503.

```sh
consumer "$BASE/v1/transactions/base/$TX_HASH"
consumer "$BASE/v1/transactions/base/$TX_HASH?fallback_only=true"
```

<a id="trust"></a>

### Cuándo verificar una transacción

Antes de abonar un pago, compruebe la ejecución, las confirmaciones, el destinatario, el activo y el importe.

[Lista negra de nodos](#blacklist)

<a id="subscriptions"></a>

## Reemplazar suscripciones

Envía la lista **completa** del consumidor como un array JSON de hasta 8 MiB. Cada elemento requiere las cadenas `network` y `address`: el identificador exacto de la red y una dirección válida en ella. Omitir una suscripción anterior la elimina. El array vacío elimina todas y conserva las entregas ya en cola.

<a id="replace-watches"></a>

### `PUT /v1/subscriptions`

Cada registro tiene una altura inicial inclusiva, `from_height`. En una red activa, una dirección nueva empieza después del último bloque asignado. Al retirar una dirección, `to_height` incluye el mayor entre la altura observada y el último bloque asignado. Los datos inválidos devuelven **400**. Si falta una altura necesaria, **409** conserva la lista anterior completa.

```sh
consumer -X PUT "$BASE/v1/subscriptions" --data '[{"network":"base","address":"0x1111111111111111111111111111111111111111"}]'
```

```json
{"registrations":[{"network":"base","address":"0x1111111111111111111111111111111111111111","from_height":101}],"terminations":[]}
```

Un **202** indica que quedan bloques pendientes para alguna baja. Repite la misma lista completa para consultar el progreso sin cambiar el límite. Con **200**, todas las bajas del recibo han terminado. Los bloques tienen las confirmaciones exigidas, los huecos están cubiertos y los eventos en cola siguen disponibles para su lectura y acuse.

```json
{"registrations":[],"terminations":[{"network":"base","address":"0x1111111111111111111111111111111111111111","from_height":101,"to_height":110,"completed":false}]}
```

<a id="deliveries"></a>

## Leer y confirmar entregas

<a id="next"></a>

### `GET /v1/events/next`

La consulta requiere `network` con una red admitida. El entero opcional `wait` activa una espera de 1 a 20 segundos; omítelo para obtener respuesta inmediata. Devuelve **200** con la siguiente entrega pendiente o **204** si no hay ninguna. Una red ausente o desconocida o un tiempo de espera inválido devuelve **400**.

```sh
consumer "$BASE/v1/events/next?network=base&wait=15"
```

El ejemplo muestra una entrega con datos de transacción abreviados. Una transacción que coincide con varias direcciones genera una entrega por consumidor. También pueden coincidir operaciones salientes o fallidas. Antes de abonar un pago, comprueba dirección, resultado, activo e importe.

```json
{
  "id": 17,
  "activity": {
    "confirmations": 3,
    "network": "base",
    "height": 12345679,
    "tx_id": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "time_ms": 1770000000000,
    "addresses": [
      "0x1111111111111111111111111111111111111111"
    ],
    "data": {
      "transaction": {
        "hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "to": "0x1111111111111111111111111111111111111111"
      }
    }
  }
}
```

| Campo | Tipo | Comportamiento |
| --- | --- | --- |
| `id` | `int64` | ID de entrega de la cola, usado al confirmar. |
| `activity.network, activity.height` | `string, int64` | Red concreta y posición en la cadena. |
| `activity.tx_id` | `string` | Identificador de la transacción en la cadena. |
| `activity.confirmations, activity.time_ms` | `int64, int64` | Número observado de confirmaciones y tiempo de la cadena en milisegundos Unix. |
| `activity.addresses` | `string[]` | Direcciones suscritas que coincidieron para este consumidor. |
| `activity.data` | `JSON` | Datos JSON propios de la red. En EVM pueden incluir `transaction`, `logs`, `receipt` y `trace`. |
| `activity.data.refresh_assets` | `string[]` | Contratos de tokens cuyos saldos deben actualizarse tras un evento global reconocido. Se omite si está vacío. [EVM](../guide/en.md#asset-events) |

<a id="ack"></a>

### `POST /v1/events/{id}/ack`

Incluye el `id` positivo de la entrega en la ruta, sin cuerpo. La confirmación solo afecta al consumidor autenticado y devuelve **204**, incluso al repetirla. Un ID inválido o no positivo devuelve **400**. Leer conserva la entrega en la cola. Guarda los efectos antes de confirmar y evita duplicados mediante la red y el identificador de transacción o transferencia.

```sh
ID=17
consumer -X POST "$BASE/v1/events/$ID/ack"
```

<a id="height"></a>

### `GET /v1/heights/{network}`

Devuelve **200** y un entero JSON con la mayor altura consecutiva completada, o `-1` si no existe cursor. Si falta un bloque anterior, pueden haberse entregado eventos de mayor altura. Aptos usa versiones del libro mayor, Solana slots y Sui checkpoints.

```sh
consumer "$BASE/v1/heights/base"
```

```json
12345677
```

<a id="streaming"></a>

## Suscripciones en tiempo real

### De ChainScanner a tu aplicación

Abre un WebSocket o un flujo gRPC bidireccional para recibir la actividad de las direcciones en cuanto entre en la cola. Ambos comparten token, suscripciones y confirmaciones con el sondeo HTTP prolongado. Usa un lector por consumidor y red entre todos los transportes. Varios consumidores pueden suscribirse a la misma dirección de forma independiente.

**Conexión.** Ejecuta uno de los comandos desde la raíz del repositorio con el token de la guía rápida. WebSocket usa HTTP/1.1 Upgrade y tramas de texto JSON. gRPC usa HTTP/2 en el mismo puerto y el esquema protobuf publicado. Introduce los mensajes de forma interactiva y mantén la entrada abierta. Para un despliegue remoto usa WSS o gRPC con TLS y configura el proxy para reenviar la negociación WebSocket y los flujos gRPC HTTP/2.

[activity.proto](../../api/activity/v1/activity.proto)

```sh
websocat -H="Authorization: Bearer $TOKEN" ws://127.0.0.1:9081/v1/events/stream
```

```sh
grpcurl -plaintext -import-path . -proto api/activity/v1/activity.proto -H "authorization: Bearer $TOKEN" -d @ 127.0.0.1:9081 chainscanner.activity.v1.ActivityFeed/Subscribe
```

**Apertura → recepción.** Envía primero la red configurada. Incluye en `replace` la lista completa solo para sustituir las suscripciones del consumidor en **todas las redes**. Una lista vacía las borra. Omite `replace` al reconectar para conservarlas. El servidor responde con `ready`. Al reemplazar, devuelve cada dirección normalizada y la altura inicial inclusiva. Al reanudar, el recibo está vacío.

Al reemplazar la lista por streaming, los límites aparecen en `ready.terminations`. En ProtoJSON, `toHeight` es una cadena decimal y la ausencia de `completed` significa falso.

```json
{"open":{"network":"base","replace":{"watches":[{"network":"base","address":"0x1111111111111111111111111111111111111111"}]}}}
```

```json
{
  "ready": {
    "registrations": [
      {
        "network": "base",
        "address": "0x1111111111111111111111111111111111111111",
        "fromHeight": "12345678"
      }
    ]
  }
}
```

**Entrega → persistencia → confirmación.** Cada flujo mantiene una entrega pendiente de confirmar. Interpreta `dataJson` según la cadena y guarda el resultado de forma duradera. Envía después un `ack` con el ID recibido por la misma conexión. La siguiente entrega llega cuando esté disponible. ProtoJSON representa los enteros de 64 bits como cadenas decimales. Una cola vacía no genera mensajes de aplicación.

```json
{
  "delivery": {
    "id": "17",
    "network": "base",
    "height": "12345679",
    "txId": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "timeMs": "1770000000000",
    "confirmations": "3",
    "addresses": [
      "0x1111111111111111111111111111111111111111"
    ],
    "dataJson": "{\"transaction\":{\"to\":\"0x1111111111111111111111111111111111111111\"}}"
  }
}
```

```json
{"ack":"17"}
```

**Desconexiones y errores.** Reconecta con espera progresiva y vuelve a enviar `open`. Una entrega pendiente puede repetirse incluso en otro transporte. Deduplica por red e identificador de transacción o transferencia. Un token inválido devuelve HTTP 401 al abrir WebSocket o `UNAUTHENTICATED` en gRPC. Una red o confirmación incorrecta produce 1008 o `INVALID_ARGUMENT`. Si falta la altura inicial, se devuelve 1013 o `FAILED_PRECONDITION`. Un fallo temporal de almacenamiento produce 1013 o `UNAVAILABLE`. Ajusta el tamaño máximo de recepción del cliente gRPC al mayor volumen de datos de transacción previsto.

```json
{"open":{"network":"base"}}
```

El cliente WebSocket debe responder a las tramas Ping con Pong. Usa un cliente de backend que pueda establecer la cabecera Authorization. El constructor WebSocket nativo del navegador no permite añadir esa cabecera. Con gRPC, mantén abierto el lado de envío para confirmar y cancela la llamada para detener un flujo inactivo. Los servidores Go que integren el módulo deben habilitar HTTP/2 para gRPC. El binario independiente ya habilita HTTP/2 sin cifrar en su puerto.

### Sondeo HTTP prolongado

Las aplicaciones reciben las transacciones mediante sondeo HTTP prolongado. Registra la lista completa de direcciones y ejecuta un lector por consumidor y red. Configura tiempos de espera del cliente y del proxy superiores a `wait`. El ejemplo usa el token de la guía rápida, Python 3 y una base SQLite local. Tras un 204 vuelve a consultar de inmediato. Al recibir una entrega, la confirma después de guardar los datos.

```sh
export BASE TOKEN
python3 - <<'PY'
import http.client
import json
import os
import sqlite3
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

base = os.environ["BASE"].rstrip("/")
headers = {"Authorization": "Bearer " + os.environ["TOKEN"]}

def request(path, method="GET"):
    req = Request(base + path, headers=headers, method=method)
    with urlopen(req, timeout=30) as res:
        if res.status != 204:
            return json.load(res)

db = sqlite3.connect("base-inbox.sqlite")
db.execute("""CREATE TABLE IF NOT EXISTS inbox (
    network TEXT, tx_id TEXT, activity TEXT NOT NULL, PRIMARY KEY (network, tx_id)
)""")
delay = 1
while True:
    try:
        delivery = request("/v1/events/next?network=base&wait=20")
        if delivery is not None:
            activity = delivery["activity"]
            with db:
                db.execute("INSERT INTO inbox VALUES (?, ?, ?) "
                           "ON CONFLICT(network, tx_id) DO NOTHING",
                           (activity["network"], activity["tx_id"], json.dumps(activity)))
            request("/v1/events/{}/ack".format(delivery["id"]), "POST")
        delay = 1
    except HTTPError as err:
        if err.code != 429 and err.code < 500:
            raise
    except (URLError, TimeoutError, ConnectionError, http.client.IncompleteRead):
        pass
    else:
        continue
    time.sleep(delay)
    delay = min(30, 2 * delay)
PY
```

El ejemplo guarda una fila por red y transacción para este consumidor. Si procesas cada transferencia por separado, incluye su índice en la clave de deduplicación. Una confirmación perdida puede causar una nueva entrega. Al reiniciar el escáner en memoria se pierden las suscripciones y entregas pendientes. Registra de nuevo la lista completa y reconcilia el intervalo con tu propio punto de control y el historial de la cadena. SQLite en archivo conserva las suscripciones y la cola entre reinicios.

### Del nodo a ChainScanner

Elige la interfaz del nodo para la red concreta. Al recibir un aviso de cabecera, el escáner lee los datos de la cadena. Tras una desconexión, recupera los intervalos pendientes a partir de la última altura completada sin huecos. Mientras la fuente esté activa, reintenta la conexión al cabo de cinco segundos. El escáner queda en reposo si la red no tiene direcciones vigiladas.

| Red | Avisos de avance | Fuente del historial |
| --- | --- | --- |
| Redes EVM | WebSocket JSON-RPC · `eth_subscribe("newHeads")` | RPC de ejecución del mismo nodo |
| Avalanche | WebSocket oficial de C-Chain · `newHeads` | El nodo HTTP integrado verifica el ID de cadena `43114` y lee bloques y transacciones |
| Solana | WebSocket · `rootSubscribe` o flujo de slots finalizados de Yellowstone gRPC | HTTP JSON-RPC en `history_url` |
| Sui | gRPC · `SubscribeCheckpoints` | Lectura de checkpoints en el mismo nodo gRPC |
| Aptos | Flujo de transacciones RawData gRPC | REST de un nodo completo en `history_url` |

```json
{
  "name": "solana",
  "protocol": "ws",
  "url": "wss://api.mainnet-beta.solana.com",
  "history_url": "https://api.mainnet-beta.solana.com"
}
```

**Ethereum Beacon (opcional).** Un nodo Beacon puede enviar avisos de cabecera por una respuesta HTTP abierta mediante Server-Sent Events (SSE). Configura `protocol` como `sse`, `url` con la ruta `/eth/v1/events` del nodo y `history_url` con un RPC HTTP de la capa de ejecución de Ethereum. El escáner se suscribe al tema `head` y registra su posición en alturas de bloques de ejecución. Un nodo de ejecución con WebSocket también puede proporcionar estos avisos por sí solo.

<a id="nodes"></a>

## Administración de nodos

<a id="list"></a>

### `GET /v1/admin/nodes`

La consulta opcional `network` filtra el inventario. Devuelve **200** con un array de nodos y sus últimas mediciones guardadas. Listar no consume cuota RPC. Si no hay coincidencias devuelve `[]`.

```sh
admin "$BASE/v1/admin/nodes?network=base"
```

```json
[
  {
    "id": 42,
    "network": "base",
    "protocol": "http",
    "url": "https://rpc.example.org",
    "origin": "custom",
    "status": "untested",
    "height": null,
    "checked_at_ms": null,
    "latency_ms": null,
    "height_unit": "block",
    "scannable": true
  }
]
```

| Campo | Tipo | Comportamiento |
| --- | --- | --- |
| `id, network` | `int64, string` | ID numérico estable y red concreta. |
| `protocol, url` | `string, string` | Transporte seleccionado y URL del nodo. |
| `origin` | `string` | Origen: `builtin`, `config`, `custom` o `chainlist`, `openchainbench`, `comparenodes`, `fallback`. |
| `status` | `string` | Uno de `untested`, `ok`, `timeout`, `unavailable`. Compruébalo después de la prueba. |
| `height, height_unit` | `int64? , string` | Altura del nodo o null. La unidad es `block`, `slot`, `checkpoint`, `ledger_version` o `beacon_slot`. |
| `checked_at_ms, latency_ms` | `int64?, int64?` | Tiempo de inicio en milisegundos Unix y duración en milisegundos. Ambos son null antes de la primera prueba. |
| `read_failure, read_retry_at_ms` | `string?, int64?` | Un fallo de lectura da `status: unavailable`. La causa se conserva tras consultar la altura y se borra cuando una lectura tiene éxito. Durante la pausa se incluye su fin en milisegundos Unix. |
| `scannable` | `bool` | Indica si el nodo puede escanear transacciones. Los nodos que solo informan de la altura no reciben tareas de escaneo. |

<a id="add"></a>

### `POST /v1/admin/nodes`

Envía las cadenas JSON `network`, `url` y especifica `protocol`. Las cadenas opcionales son `api_key`, `history_url` y `history_api_key`; los transportes emparejados requieren una URL histórica. Los campos desconocidos, opciones inválidas o JSON incorrecto devuelven **400**. El límite es 8 KiB. Devuelve **201** con el nodo o **409** si ya existe. Añadir el nodo no activa por sí solo el lector de la red.

```sh
admin -X POST "$BASE/v1/admin/nodes" --data '{"network":"base","protocol":"http","url":"https://rpc.example.org"}'
```

<a id="fallback"></a>

### `POST /v1/admin/nodes/fallback`

Registra un nodo de historial de pago para una red con los mismos campos y el token de administrador de la ruta normal. Devuelve **201** con `origin: fallback`. Tras un fallo de lectura, un nodo de respaldo libre puede tomar el intervalo y verificar sus confirmaciones. También repara huecos guardados en SQLite tras reiniciar. Las lecturas iniciales y las consultas de altura para suscripciones usan nodos ordinarios. Las pruebas manuales pueden consumir cuota. Los huecos pendientes se reintentan y tres fallos consecutivos activan la pausa progresiva. Usa el ID devuelto para probar o eliminar el nodo. Registre cada respaldo por separado. Los intervalos fallidos se reparten entre los respaldos aptos.

```sh
admin -X POST "$BASE/v1/admin/nodes/fallback" --data '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
admin -X POST "$BASE/v1/admin/nodes/fallback" --data '{"network":"base","protocol":"http","url":"https://second-rpc.example.net","api_key":"SECOND_PROVIDER_KEY"}'
```

<a id="scan-progress"></a>

### `GET /v1/admin/progress`

Devuelve una fila por red configurada sin consultar los nodos. `target_height` incluye las confirmaciones y `cursor_height` indica hasta dónde se ha escaneado sin huecos. `lag_ms` mide el tiempo mínimo observado que lleva pendiente una altura elegible. El historial en memoria se reinicia al arrancar o al iniciar otra suscripción tras quedar la red inactiva. El estado es `warning` por encima de 300 000 ms y `ok` en otro caso. Para `idle`, `warming` y `stale` el retraso es null.

`scan_success_count` y `scan_failure_count` cuentan los rangos escaneados con éxito y los fallidos durante el período de seguimiento actual. Los reintentos con otros nodos producen un solo resultado por rango. Un intento de recuperación posterior produce otro resultado. Las consultas de altura, las pruebas de nodos, la inactividad y las cancelaciones no añaden muestras. Ambos contadores se reinician a cero al finalizar la última suscripción o al reiniciar el servicio. Las versiones anteriores omiten estos campos.

<a id="scan-retry-rate"></a>

### `GET /v1/admin/scan-retry-rate`

Requiere el token de administrador. Sin `network` devuelve todas las redes configuradas. Un filtro vacío, repetido o con una red no configurada devuelve **400**.

`total_attempts` cuenta las lecturas de rangos iniciadas después de comprobar la altura del nodo. `retry_attempts` cuenta las relecturas y los intentos de reparar rangos marcados como fallidos, incluso con otro proveedor. Cada intento cuenta una vez aunque cubra varias alturas o llamadas RPC. Se excluyen las consultas de altura, las consultas por hash y los reintentos de entrega. Los contadores se conservan en memoria entre periodos de vigilancia y se reinician con el servicio. `retry_rate` es `retry_attempts / total_attempts` (0–1), o `null` si aún no hubo lecturas.

```sh
admin "$BASE/v1/admin/scan-retry-rate?network=base"
```

```json
[{"network":"base","total_attempts":100,"retry_attempts":4,"retry_rate":0.04}]
```

<a id="test-one"></a>

### `POST /v1/admin/nodes/{id}/test`

El `id` de la ruta debe ser entero positivo. Sin cuerpo. Devuelve **200** con el nodo actualizado, incluso si la prueba registró `timeout` o `unavailable`. ID inválido: **400**. ID desconocido o eliminado: **404**.

```sh
NODE_ID=$(curl -fsS -H "Authorization: Bearer $ADMIN" "$BASE/v1/admin/nodes?network=base" | jq -er '.[] | select(.url == "https://rpc.example.org" and .origin == "custom") | .id') &&
admin -X POST "$BASE/v1/admin/nodes/$NODE_ID/test"
```

<a id="test-all"></a>

### `POST /v1/admin/nodes/test`

Prueba todos los nodos visibles, con filtro opcional `network`. Sin cuerpo. Devuelve **200** con un array que incluye las pruebas fallidas. Hay hasta ocho pruebas en paralelo. Una segunda petición de lote simultánea devuelve **409**. Cancelar la petición detiene el lote.

```sh
admin -X POST "$BASE/v1/admin/nodes/test?network=base"
```

<a id="delete"></a>

### `DELETE /v1/admin/nodes/{id}`

Elimina un nodo por `id` positivo, incluidos los integrados o configurados. Devuelve **204**, **400** para un ID inválido o **404** si el nodo no existe. El modo con archivo conserva la eliminación tras reiniciar. Volver a registrar el endpoint permite restaurarlo.

```sh
admin -X DELETE "$BASE/v1/admin/nodes/$NODE_ID"
```

<a id="blacklist"></a>

### Lista negra de nodos

Usa el token de administración. Un dominio incluye sus subdominios en todas las redes y puertos. Una IP se compara de forma exacta. Se revisan las URL principal e histórica al seleccionar nodos y sondear candidatos. La regla no descubre operadores tras alias DNS, proxies o configuraciones globales TON.

Usa `keyword:acme` para una subcadena del nombre de host sin distinguir mayúsculas, `exact:rpc.example.org` para un host exacto o un dominio sin prefijo para incluir subdominios. Usa el mismo formato en la configuración y en ambas rutas. Se excluyen rutas, credenciales y parámetros.

```sh
admin -X PUT "$BASE/v1/admin/blacklist/keyword:acme"
admin -X PUT "$BASE/v1/admin/blacklist/exact:rpc.example.org"
admin "$BASE/v1/admin/blacklist"
admin -X DELETE "$BASE/v1/admin/blacklist/keyword:acme"
admin -X DELETE "$BASE/v1/admin/blacklist/exact:rpc.example.org"
```

GET devuelve una lista ordenada de `{host, source}`. PUT y DELETE son idempotentes y devuelven 204. Las nuevas selecciones aplican la regla y el escáner actualiza su grupo. SQLite en archivo conserva las reglas. Fija reglas con `"blacklist": ["rpc.example.org"]` en la raíz de la configuración. DELETE devuelve 409 para reglas de configuración. Para retirarlas, edita el archivo y reinicia.

<a id="errors"></a>

## Manejo de errores

Corrige la petición antes de repetir un **400**. Para **401**, revisa token y rol. El significado de **409** depende de la ruta, como se indica arriba. **503** señala un fallo al acceder a suscripciones, cola, cursor o inventario; reintenta los fallos transitorios con espera progresiva. Una cola vacía devuelve **204**.

[Guía](../../README.es.md) · [ConsumerHandler](https://github.com/GMWalletApp/ChainScanner/blob/main/internal/feed/handler.go) · [NodeRegistry.Handler](https://github.com/GMWalletApp/ChainScanner/blob/main/internal/nodes/http.go)
