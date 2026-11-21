# Manual Linux installation

[Documentation index](README.md) · [简体中文](manual_RUN.md)

Use this procedure to run the released binary under your own service manager. For a managed systemd installation, follow the [epctl guide](EPCTL.en.md).

## Prepare the application directory

Download the Linux archive for your architecture from [Releases](https://github.com/GMWalletApp/epusdt/releases). Extract it into a private application directory such as `/opt/epusdt`. Configure a dedicated service account with write access to the configuration and SQLite directories. Keep these directories outside the public web root.

## Configure and test

On a fresh installation copy the included example, then edit the copy. Preserve an existing `.env` on upgrades.

```sh
cd /opt/epusdt
chmod +x epusdt
cp -n .env.example .env
```

For a configured service behind an HTTPS proxy, set these entries in `.env` and use your public domain. The first startup creates the SQLite tables.

```dotenv
app_uri=https://pay.example.com
http_listen=127.0.0.1:8000
runtime_root_path=./runtime
sqlite_database_filename=epusdt.db
install=false
```

Run `./epusdt http start` in the application directory. From another shell check `curl -fsS http://127.0.0.1:8000/payments/gmpay/v1/config`. Stop the foreground process before starting the managed service.

## Keep the service running

For Supervisor, use the following program definition and replace `epusdt` in the user field with the service account you prepared. Run `supervisorctl reread`, then `supervisorctl update`.

```ini
[program:epusdt]
directory=/opt/epusdt
command=/opt/epusdt/epusdt http start
user=epusdt
autostart=true
autorestart=true
redirect_stderr=true
```

Point your HTTPS virtual host at `http://127.0.0.1:8000`. For Nginx, use this location inside the TLS server block. Restart the service after editing its configuration. Back up the configuration and data directories before upgrading.

```nginx
location / {
    proxy_pass http://127.0.0.1:8000;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```
