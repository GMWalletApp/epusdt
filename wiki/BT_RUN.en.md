# Install using aaPanel

[Documentation index](README.md) · [简体中文](BT_RUN.md)

## Create the site

Create a static site for your checkout domain and configure DNS and HTTPS. EPUSDT runs as a separate process with SQLite. Keep the binary, configuration and databases outside the site’s public directory.

![Add the checkout domain](img/add_web.png)

## Configure the process

1. Install the Supervisor manager in aaPanel. Download and unpack the release for your architecture. Follow the [manual installation guide](manual_RUN.en.md) to prepare the service account, writable directories and `.env`.
2. Set `app_uri` to your HTTPS checkout URL and `http_listen=127.0.0.1:8000`. In Supervisor set the working directory to your installation directory and the command to its full binary path followed by `http start`. Enable automatic start and restart.
3. Check the process status and logs in Supervisor.

![Supervisor process settings](img/supervisor.png)

## Add the reverse proxy and verify

In the site settings add a reverse proxy pointing at `http://127.0.0.1:8000`. Open `https://YOUR-DOMAIN/payments/gmpay/v1/config` and check for `status_code: 200`. Add a receiving address to make its enabled assets available at checkout. After a configuration change, restart EPUSDT from Supervisor.

![Reverse proxy settings](img/fanxiang.png)
