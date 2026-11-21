1. 创建和进去这个目录
```shell
mkdir epusdt && cd epusdt
```

2. 把配置文件放进去这个目录，只需要改

app_url

tron_grid_api_key

api_rate_url

```shell
cat <<EOF > env
app_name=epusdt
app_uri=https://dujiaoka.com
log_level=info
http_access_log=false
sql_debug=false
http_listen=:8000

static_path=/static
runtime_root_path=/app/conf/runtime

log_save_path=/logs
log_max_size=32
log_max_age=7
max_backups=3

# SQLite is the only supported database and the default.
db_type=sqlite

# sqlite primary database config
sqlite_database_filename=/app/conf/epusdt.db
sqlite_table_prefix=

# sqlite runtime store config
runtime_sqlite_filename=epusdt-runtime.db

# background scheduler config
queue_concurrency=10
queue_poll_interval_ms=1000
callback_retry_base_seconds=5

tg_bot_token=
tg_proxy=
tg_manage=

api_auth_token=

order_expiration_time=10
order_notice_max_retry=0
api_rate_url=https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/
tron_grid_api_key=
EOF
```
3. docker compose 创建
```shell
cat <<EOF > docker-compose.yaml
services:
  epusdt:
    image: gmwallet/epusdt:latest
    restart: always
    build:
      context: .
      dockerfile: Dockerfile
    volumes:
      - ./env:/app/.env
      - ./data:/app/conf
    ports:
      - "8000:8000"
EOF
```
4. 运行
```shell
docker compose up -d
```
5. 配置独角兽后台

商户密钥： http://your_domain/payments/epusdt/v1/order/create-transaction
