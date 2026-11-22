# EPUSDT documentation

[Project home](../README.md) · [简体中文](README.zh_CN.md)

## Integrate payments

- [Merchant API and callbacks](API.en.md)

- [Migrate a legacy plugin](LEGACY_API_MIGRATION.en.md)

- [Python order example](../sdk/python/gmpay_create_order.py)

- [OpenAPI specification](../src/docs/swagger.json)

## Install and operate

- [Linux with epctl](EPCTL.en.md)

- [Docker Compose](docker-RUN.en.md)

- [Manual Linux installation](manual_RUN.en.md)

- [aaPanel](BT_RUN.en.md)

## ChainScanner

Use an EPUSDT admin JWT with `GET /admin/api/v1/chainscanner/admin/scan-retry-rate` to read per-network block scan retry rates. Add `?network=base` to query Base. The rate is retries divided by total read attempts, or `null` before the first read. Counters reset when the scanner restarts. An older external scanner without the endpoint returns HTTP 404. [Counting rules](../chainscanner/docs/api/en.md#scan-retry-rate)

The scanner also provides [Spanish](../chainscanner/README.es.md), [Russian](../chainscanner/README.ru.md) and [Vietnamese](../chainscanner/README.vi.md) editions.

- [Scanner overview](../chainscanner/README.md)

- [Operator guide](../chainscanner/docs/guide/en.md)

- [Scanner API](../chainscanner/docs/api/en.md)

## Maintain the project

- [Development standard](../DEVELOPMENT_STANDARD.md)

- [Publish a release](../RELEASING.md)

- [Historical SQLite migration review](../REVIEW.md)
