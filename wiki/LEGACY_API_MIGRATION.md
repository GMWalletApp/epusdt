# 迁移旧版商户接口

[文档目录](README.zh_CN.md) · [English](LEGACY_API_MIGRATION.en.md)

## 确认插件使用的协议

先确认插件（包括独角数卡插件）支持的协议。旧指南曾使用 `/api/v1/order/create-transaction` 或 `/payments/epusdt/v1/order/create-transaction`。当前代码注册的路由如下。只支持旧签名的插件还需升级签名逻辑。

| 协议 | 创建订单 | 请求与回调签名 |
| --- | --- | --- |
| GMPay | `POST /payments/gmpay/v1/order/create-transaction` | HMAC-SHA256 |
| EPay | `GET/POST /payments/epay/v1/order/create-transaction/submit.php` | MD5 |

## 切换前验证

1. 从后台获取商户 PID 与密钥，EPay 使用数字 PID。
2. 通过 `GET /payments/gmpay/v1/config` 查询可用网络和币种。GMPay 如需买家在收银台选择，可同时省略两项。
3. 对照[商户 API 示例](API.md)验算签名，联调创建订单、浏览器跳转与签名回调。重复通知应幂等处理，成功时返回 HTTP 200 及 `ok` 或 `success`。

同时变更数据目录或数据库的部署，请参考[升级说明](../README.zh_CN.md#部署与升级)。

## 后台节点管理升级

「区块链 → 节点管理」通过 EPUSDT 管理员登录操作当前 ChainScanner。内置服务随 EPUSDT 启动。独立部署时，在后端设置 `CHAINSCANNER_URL`、`CHAINSCANNER_TOKEN` 和独立的 `CHAINSCANNER_ADMIN_TOKEN`。新版两种部署使用相同的节点操作。概览显示当前目标，旧版外部服务仍可列出、新增、删除和检查节点。

旧接口 `/admin/api/v1/rpc-nodes` 已移除。以下路径以前缀 `/admin/api/v1/chainscanner/admin` 开头，使用管理员 JWT，响应为原始 JSON 或 204。

| 操作 | 替代接口 |
| --- | --- |
| 当前服务及支持的操作 | `GET /connection` |
| 列表 | `GET /nodes`，可按 `network` 过滤 |
| 新增普通或兜底节点 | `POST /nodes` 或 `POST /nodes/fallback` |
| 编辑、启用或停用 | `PATCH /nodes/{id}`，例如 `{"enabled":false}` |
| 删除 | `DELETE /nodes/{id}` |
| 健康检查 | `POST /nodes/{id}/test` 或 `POST /nodes/test`，批量支持 `network` 和 `include_manual=true` |
| 追块进度 | `GET /progress` |
| 预览及执行旧节点迁移 | `GET /migration`，确认后 `POST /migration`，提交预览中的 `target` 和 `store_id` |

内置模式自动导入旧库。外部模式先预览目标、待迁移记录和冲突，确认后才上传节点配置。完整地址中的路径和查询参数参与匹配。同一链、协议、地址的等价记录共用一个节点，凭据、角色、权重、启停或用途冲突的记录留待核对。迁移可重试，旧表保留作核对。普通节点负责日常追块，兜底节点优先参与缺块补扫及严格验单。补单专用节点仅供显式检查和人工补单使用。
