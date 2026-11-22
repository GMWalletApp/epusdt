# EPUSDT 文档目录

[项目首页](../README.zh_CN.md) · [English](README.md)

## 接入收款

- [商户 API 与回调](API.md)

- [迁移旧版插件](LEGACY_API_MIGRATION.md)

- [Python 创建订单示例](../sdk/python/gmpay_create_order.py)

- [OpenAPI 规范](../src/docs/swagger.json)

## 部署与运维

- [Linux 安装管理工具 epctl](EPCTL.md)

- [Docker Compose 部署](docker-RUN.md)

- [手动部署 Linux 二进制](manual_RUN.md)

- [宝塔面板部署](BT_RUN.md)

## ChainScanner

使用 EPUSDT 管理员 JWT 请求 `GET /admin/api/v1/chainscanner/admin/scan-retry-rate` 可查询各链扫块重试率。加 `?network=base` 只查 Base。`retry_rate` 等于重试读取次数除以总读取次数，尚未扫块时为 `null`，扫描器重启后重新计数。旧版外部扫描器缺少此接口时返回 HTTP 404。[扫块统计口径](../chainscanner/docs/api/zh-CN.md#scan-retry-rate)

扫描器另有[西班牙语](../chainscanner/README.es.md)、[俄语](../chainscanner/README.ru.md)和[越南语](../chainscanner/README.vi.md)版本。

- [扫描器概览](../chainscanner/README.zh-CN.md)

- [运行指南](../chainscanner/docs/guide/zh-CN.md)

- [扫描器 API](../chainscanner/docs/api/zh-CN.md)

## 项目维护

- [开发规范](../DEVELOPMENT_STANDARD.zh_CN.md)

- [发布发行版](../RELEASING.zh_CN.md)

- [SQLite 运行时迁移评审记录](../REVIEW.zh_CN.md)
