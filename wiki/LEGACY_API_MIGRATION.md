# API 变更文档

## 概述

## 后台配置说明（重要）

### 对于 dujiaoka 用户

**只需要修改一个地方**：在 dujiaoka 后台支付插件配置中，将 API 地址前缀从 `/api` 改为 `/payments/epusdt` 即可。

**示例**：

```
旧配置：https://your-domain.com/api/v1/order/create-transaction
新配置：https://your-domain.com/payments/epusdt/v1/order/create-transaction
```

**就这么简单！** 其他配置（密钥、回调地址等）完全不需要修改。

---

## 路由变更对照表

### API 路由

| 原路由 | 新路由 | 说明 |
|--------|--------|------|
| `POST /api/v1/order/create-transaction` | `POST /payments/epusdt/v1/order/create-transaction` | 创建交易订单 |


## 新增配置项说明

本次更新新增了以下配置项（位于 `src/.env.example` 文件）：

### 1. `api_rate_url` - 汇率接口 URL

用于获取实时汇率的 API 地址。系统会根据此接口动态获取不同币种的汇率。

```bash
# 汇率接口url
api_rate_url=https://your-rate-api.com/
```

**API 格式要求**：

系统会请求 `{api_rate_url}/{currency}.json`，例如：
- `https://your-rate-api.com/cny.json`
- `https://your-rate-api.com/usd.json`

**返回格式示例**：

```json
{
  "cny": {
    "usdt": 0.1389,
    "trx": 0.0123
  }
}
```

其中 `0.1389` 表示 1 CNY = 0.1389 USDT（即 1 USDT ≈ 7.2 CNY）

**说明**：
- 支持自建汇率 API，只需按照上述格式返回数据即可

### 2. `tron_grid_api_key` - TRON Grid API Key

TRON Grid API 密钥，用于提高 API 请求限制和稳定性。

```bash
tron_grid_api_key=
```

**如何获取 API Key**：

1. 访问 [https://www.trongrid.io/](https://www.trongrid.io/)
2. 注册账号并登录
3. 在控制台创建 API Key
4. 将 API Key 填入配置文件

**为什么需要 API Key**：

- ✅ **提高请求限制**：免费账号有更高的 API 调用配额
- ✅ **更好的稳定性**：避免公共 API 的限流问题
- ✅ **支持更多功能**：为后续支持 TRX 等其他代币做准备

### 配置示例

`.env` 配置示例：

```bash
# 订单过期时间（分钟）
order_expiration_time=15

# 订单回调失败最大重试次数
order_notice_max_retry=0

# 汇率接口url（动态获取汇率）
api_rate_url=

# TRON Grid API Key（推荐配置）
tron_grid_api_key=your-api-key-here
```

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
