# epctl 安装与验证脚本

`epctl` 是仓库顶层的 Linux 二进制安装管理脚本，面向已经发布到 GitHub Releases 的 `epusdt` 二进制包。
`epctl-docker-test.sh` 是配套的真实验收脚本，用本机 Docker 启动 Ubuntu + systemd 容器，完整验证安装、启动、升级和初始化密码流程。

## 适用范围

- 仅支持 Linux
- 仅支持二进制安装
- 安装源固定为 `https://github.com/GMWalletApp/epusdt/releases`
- 默认通过 systemd 管理服务

## 依赖与权限

`epctl` 依赖这些基础命令：

- `curl`
- `tar`
- `systemctl`
- `install`
- `grep`
- `sed`

其中：

- `install`、`upgrade`、`self-install` 需要写入 `/opt`、`/etc/systemd`、`/usr/local/bin`
- `status`、`logs` 会在需要时自动通过 `sudo` 重新执行
- 所以日常使用建议当前用户具备 `sudo` 权限

## 固定路径

| 项目 | 路径 |
|------|------|
| 安装目录 | `/opt/epusdt` |
| 主程序 | `/opt/epusdt/epusdt` |
| 配置文件 | `/opt/epusdt/.env` |
| 示例配置 | `/opt/epusdt/.env.example` |
| 前端释放目录 | `/opt/epusdt/www` |
| 下载缓存 | `/tmp/epusdt/<tag>/` |
| systemd unit | `/etc/systemd/system/epusdt.service` |
| epctl 全局安装位置 | `/usr/local/bin/epctl` |

## 快速开始

直接在仓库根目录运行交互菜单：

```bash
./epctl
```

默认优先进入中文界面。你也可以显式指定语言：

```bash
./epctl zh
./epctl en
./epctl --lang zh help
./epctl --lang en help
```

如果想把脚本装进 PATH：

```bash
./epctl self-install
epctl
```

## 常用命令

下载指定版本：

```bash
./epctl download --tag v1.0.8
```

安装服务：

```bash
./epctl install --tag v1.0.8 \
  --app-uri https://pay.example.com \
  --listen 127.0.0.1:18000
```

升级到新版本：

```bash
./epctl upgrade --tag v1.0.9
```

直接执行 `./epctl upgrade --tag ...` 时，脚本会在文件替换完成后默认立即执行 `systemctl restart epusdt`。
如果你只想替换文件而不重启，请显式传入 `--no-restart`。
如果你希望人工确认，再传 `--prompt-restart`；交互终端下提示为 `[Y/n]`，直接回车默认重启。

查看配置、状态、日志：

```bash
./epctl show-config
./epctl status
./epctl logs --lines 200
```

请求初始化管理员密码：

```bash
./epctl init-password
```

## 不传 `--tag` 时的行为

`download`、`install`、`upgrade` 在未传 `--tag` 时，会先调用 GitHub API 解析当前 latest release tag，再向用户显示实际 tag 并确认。

例如：

```bash
./epctl install --app-uri https://pay.example.com
```

交互模式下会先提示检测到的最新 tag。
非交互脚本执行时，建议显式传入 `--tag`。如果你明确要跳过确认，可以设置：

```bash
EPCTL_ASSUME_YES=1 ./epctl download
```

## 首次安装时会发生什么

执行 `install` 时，脚本会：

1. 按当前机器架构下载 GitHub Release 压缩包
2. 解压到 `/tmp/epusdt/<tag>/extract/`
3. 安装二进制到 `/opt/epusdt/epusdt`
4. 安装 `.env.example` 到 `/opt/epusdt/.env.example`
5. 创建系统用户和组 `epusdt`
6. 若 `/opt/epusdt/.env` 不存在，则从 `.env.example` 自动生成
7. 写入并启用 `epusdt.service`

自动生成 `.env` 时，脚本只会补默认上线所需的最小改动：

- `install=false`
- `app_uri=<--app-uri，默认 http://127.0.0.1:8000>`
- `http_listen=<--listen，默认 127.0.0.1:8000>`

如果 `/opt/epusdt/.env` 已存在，则安装和升级都会保留它，不会覆盖。
`/opt/epusdt/.env.example` 则会在每次 install / upgrade 时按当前 release 重新刷新。

## 升级时会发生什么

执行 `upgrade` 时，脚本会：

1. 按当前机器架构下载目标 GitHub Release 压缩包
2. 解压到 `/tmp/epusdt/<tag>/extract/`
3. 要求现有 `/opt/epusdt/.env` 已存在；若不存在会直接失败，并提示先执行 `install`
4. 覆盖 `/opt/epusdt/epusdt`
5. 覆盖 `/opt/epusdt/.env.example`
6. 保留现有 `/opt/epusdt/.env`
7. 刷新 `epusdt.service` 并执行 `systemctl daemon-reload`
8. 默认立即执行 `systemctl restart epusdt`

补充行为：

- `upgrade` 不会再补写 `.env`，也不会再执行 `systemctl enable`
- `upgrade --no-restart` 只替换文件，不重启服务，并输出手动 restart 提示
- `upgrade --prompt-restart` 会在交互终端下询问是否重启
- 如果升级后的重启失败，脚本会尝试回滚旧的二进制、`.env.example` 和 unit 文件

## systemd 服务说明

脚本注册的服务名固定为 `epusdt.service`，核心参数如下：

```ini
WorkingDirectory=/opt/epusdt
ExecStart=/opt/epusdt/epusdt http start
User=epusdt
Group=epusdt
Restart=always
RestartSec=3
```

`WorkingDirectory` 固定为 `/opt/epusdt`，因为程序会在二进制同级目录释放 `www/` 静态文件。

## `init-password` 的含义

`epctl init-password` 只会请求本地 HTTP 路由：

```text
GET /admin/api/v1/auth/init-password
```

它不会直接读数据库。

脚本会从 `/opt/epusdt/.env` 解析 `http_listen`，然后自动把这些监听写法转成本地可请求地址：

- `:8000` -> `127.0.0.1:8000`
- `0.0.0.0:8000` -> `127.0.0.1:8000`

如果接口返回 `10040`，含义是初始化明文密码已经不可用。常见原因是：

- 管理员已经登录并修改过密码
- 初始化密码已经被消费，当前不再允许再次取回

此时脚本会直接把接口原始错误输出出来，方便排查。

## Docker 验收脚本

仓库顶层提供：

```bash
./epctl-docker-test.sh <install-tag> [upgrade-tag]
```

示例：

```bash
./epctl-docker-test.sh v1.0.6
./epctl-docker-test.sh --lang zh v1.0.6 v1.0.8
```

它会在本机：

- 直接从 `ubuntu:24.04` 启动容器，并在容器启动阶段安装 systemd 与测试依赖
- 启动一个特权容器
- 在容器内执行 `epctl self-install`
- 下载真实 GitHub Release
- 安装 `epusdt`
- 若传入 `upgrade-tag`，验证 `upgrade --no-restart`、默认 `upgrade`、以及 `upgrade --prompt-restart` 的 `n/回车` 分支
- 检查 `systemd` 服务、`www/index.html`、配置文件、日志、状态输出
- 验证 `init-password` 首次成功、修改管理员密码后再次请求返回 `10040`

运行前提：

- 本机已安装 Docker
- 当前用户有权限执行 Docker
- 宿主机能够访问 GitHub Releases

## 建议

- 自动化部署场景优先显式传 `--tag`
- 生产环境建议安装完成后先执行一次 `./epctl show-config`
- 首次拿到初始化密码后，建议立即登录后台修改管理员密码
- 如果只是验证脚本是否可用，优先跑 `./epctl-docker-test.sh`
