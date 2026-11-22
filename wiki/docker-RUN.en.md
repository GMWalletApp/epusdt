# Install with Docker Compose

[Documentation index](README.md) · [简体中文](docker-RUN.md)

Use the [Compose file](../docker-compose.yaml) from the version you are installing. The default service includes ChainScanner.

## Start the gateway

From the repository root, set the public checkout URL in the Compose environment (for example, a project-level `.env`). The entrypoint uses it on each startup.

```dotenv
APP_URI=https://pay.example.com
EPUSDT_PORT=8000
```

```sh
docker compose up -d epusdt
docker compose logs --tail=100 epusdt
curl -fsS http://127.0.0.1:8000/payments/gmpay/v1/config
```

Point your HTTPS reverse proxy at the mapped port. Configure a receiving address, then check `data.supported_assets` in the response.

## Storage and upgrades

Compose mounts `./runtime/epusdt` at `/app/runtime`. That directory holds the application `.env`, business and runtime databases, and the embedded scanner database. The project-level Compose environment and the application configuration are separate files.

When upgrading from v2.0.0, stop the old service, back up its configuration and databases, and copy them to the new mount **before recreating the old container**. Preserve the original backups and check the configured database filenames. [Release notes](https://github.com/GMWalletApp/epusdt/releases/tag/v2.1.0)

## Using a standalone scanner

Set `CHAINSCANNER_URL=http://chainscanner:9081` and provide distinct consumer and admin tokens. Keep the existing scanner volume (`./runtime/chainscanner`), URL and credentials when upgrading a standalone deployment.

```sh
docker compose --profile scanner up -d
```
