# 使用 Docker Compose 部署

[文档目录](README.zh_CN.md) · [English](docker-RUN.en.md)

使用部署版本自带的 [Compose 配置](../docker-compose.yaml)。默认支付服务内置 ChainScanner。

## 启动网关

在仓库根目录设置 Compose 环境变量（例如写入项目根目录的 `.env`）。入口脚本每次启动都会读取公开收银台地址。

```dotenv
APP_URI=https://pay.example.com
EPUSDT_PORT=8000
```

```sh
docker compose up -d epusdt
docker compose logs --tail=100 epusdt
curl -fsS http://127.0.0.1:8000/payments/gmpay/v1/config
```

将 HTTPS 反向代理指向映射端口。在后台添加收款地址后，检查响应中的 `data.supported_assets`。

## 持久化与升级

Compose 将宿主机 `./runtime/epusdt` 挂载到容器的 `/app/runtime`。其中包含应用的 `.env`、业务库、运行库和内置扫描器数据库。项目根目录的 Compose 环境文件与此处的应用配置文件分别维护。

使用包含本次前端内嵌修复的镜像时，设置 `EPUSDT_READ_ONLY=true` 可将网关容器根文件系统挂载为只读。`/app/runtime` 仍需可写，以保存配置、数据库和日志。从 v2.1.1 等需要解压 `www/` 的镜像升级时，先换用新版镜像，再启用此设置。

从 v2.0.0 升级时，先停止旧服务，备份配置和数据库，**在重建旧容器之前**将数据迁移到新挂载目录。保留原备份，并核对配置中的数据库文件名。[升级说明](https://github.com/GMWalletApp/epusdt/releases/tag/v2.1.0)

## 使用独立扫描器

设置 `CHAINSCANNER_URL=http://chainscanner:9081`，配置不同的消费者令牌和管理员令牌。升级已有独立部署时，应保留原扫描器数据卷（`./runtime/chainscanner`）、连接地址和凭据。

```sh
docker compose --profile scanner up -d
```

## 收银台客服脚本

在后台「系统配置 → 基础配置 → 收银台自定义脚本」粘贴客服服务商的完整安装片段，保存后买家打开收银台即会加载。清空字段可关闭。原客服链接仍可作为备用入口。安装代码会公开发给买家浏览器，请只使用服务商的公开站点标识。

默认部署用隔离的浏览器框架运行插件。依赖第一方存储、网站 Cookie 或顶层文档的插件可使用独立收银台域名。为公开收银台分配专用 HTTPS 主机名，例如 `pay.example.com`，管理后台使用另一个主机名。将 `APP_URI` 和进程环境变量 `CASHIER_SCRIPT_ORIGIN` 都设为 `https://pay.example.com`，让反向代理保留原始 Host，然后重启 EPUSDT。专用主机拒绝管理与安装请求，插件在该收银台页面直接运行并拥有读写付款内容的能力。请仅粘贴可信来源的代码。
