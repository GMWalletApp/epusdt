# 手动部署 Linux 二进制

[文档目录](README.zh_CN.md) · [English](manual_RUN.en.md)

可按以下步骤使用自己的进程管理器运行发布包。使用 systemd 自动安装请参阅 [epctl 指南](EPCTL.md)。

## 准备应用目录

从[发布页](https://github.com/GMWalletApp/epusdt/releases)下载与服务器架构相符的 Linux 压缩包，解压到 `/opt/epusdt` 等私有应用目录。为服务账号授予配置及 SQLite 目录写权限，数据目录应位于网站公开目录之外。

## 配置并验证

首次安装时复制示例后编辑副本。升级时保留已有的 `.env`。

```sh
cd /opt/epusdt
chmod +x epusdt
cp -n .env.example .env
```

在已配置、使用 HTTPS 反向代理的服务上，将以下配置写入 `.env` 并替换为自己的域名。首次启动会创建 SQLite 表。

```dotenv
app_uri=https://pay.example.com
http_listen=127.0.0.1:8000
# 仅当应用前方有反向代理时填写代理 IP/CIDR，多个值用逗号分隔。
http_trusted_proxies=127.0.0.1/32
runtime_root_path=./runtime
sqlite_database_filename=epusdt.db
install=false
```

在应用目录执行 `./epusdt http start`。另开终端执行 `curl -fsS http://127.0.0.1:8000/payments/gmpay/v1/config`。验证后停止前台进程，再使用进程管理器启动。

## 进程守护与代理

Supervisor 可使用以下配置，按实际服务账号修改 `user`，然后执行 `supervisorctl reread` 和 `supervisorctl update`。

```ini
[program:epusdt]
directory=/opt/epusdt
command=/opt/epusdt/epusdt http start
user=epusdt
autostart=true
autorestart=true
redirect_stderr=true
```

将 HTTPS 站点的反向代理指向 `http://127.0.0.1:8000`。Nginx 可在 TLS 站点配置中加入以下片段。修改应用配置后重启服务；升级前备份配置和数据目录。

```nginx
location / {
    proxy_pass http://127.0.0.1:8000;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```
