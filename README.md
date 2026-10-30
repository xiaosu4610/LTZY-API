# LTZY-API

> 一个轻量、原生透传的大模型接口网关。PHP 8 + Swoole + PostgreSQL，单进程协程，内存占用约 47 MB。
> 支持 OpenAI / Anthropic / Gemini 多协议互转，多渠道路由、密钥池轮询、按次计费与在线充值。

[![PHP](https://img.shields.io/badge/PHP-8.2%2B-777BB4?logo=php&logoColor=white)](https://www.php.net/)
[![Swoole](https://img.shields.io/badge/Swoole-6.x-2E7D32)](https://www.swoole.com/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-14%2B-336791?logo=postgresql&logoColor=white)](https://www.postgresql.org/)
[![License](https://img.shields.io/badge/License-MIT-blue)](LICENSE)

---

## 这是什么

把你自己配置的多个上游大模型渠道，聚合成一个 OpenAI 兼容的接口。用户在任意 OpenAI 客户端里改一下 BaseURL 就能用。

```
客户端（ChatGPT-Next-Web / Cherry Studio / LobeChat / IDE 插件 …）
                    ↓  OpenAI 兼容协议
              LTZY-API 网关
                    ↓  渠道路由 + 密钥池轮询
     ┌──────────────┼──────────────┐
  上游渠道 A     上游渠道 B     上游渠道 C …
  （渠道由部署者自行在后台配置）
```

> **本项目不附带任何上游渠道、密钥或模型清单。** 部署完成后是空系统，
> 你需要自己在管理后台添加渠道（BaseURL + 密钥 + 模型列表）。
> 用哪家上游、配哪些模型、定什么价格，完全由你自己决定。

### 核心特性

| 能力 | 说明 |
| --- | --- |
| **多协议互转** | OpenAI Chat / OpenAI Responses / Anthropic Messages / Gemini，四进四出 |
| **多渠道路由** | 按模型→渠道映射，支持优先级、权重、分组倍率 |
| **密钥池** | 同一渠道多把密钥自动轮询，遇到 401/429 单独冷却，不拖累其他密钥 |
| **计费** | 按次计费（免费 / 收费双轨），支持每人独立定价 |
| **在线充值** | 易支付（epay）协议，异步回调入账，幂等防重 |
| **安装向导** | 浏览器里 4 步完成部署，装完入口自动关闭 |
| **流式透传** | SSE 逐块转发，不缓冲、不改写内容 |

### 设计原则（来自真实踩坑）

1. **网关的职责是转发，不是理解内容。** 不解析、不重组、不改写上游响应体。任何"帮用户修一下"的冲动都要先问：会不会改变客户端看到的字节？会不会破坏 `tool_calls` 结构？
2. **只允许一处改写：`model` 字段。** 用于隐藏上游真实模型名。
3. **超时必须有硬上限。** 没有上限时排队请求会一直占着并发槽，用户端表现为"一直没有响应"——既不是成功也不是失败，最难排查。
4. **协程里不做加载/解析。** Swoole 单进程环境下会触发无输出的段错误。

---

## 快速开始

### 环境要求

- PHP **8.2+**（推荐 8.3/8.4）
- 扩展：`pdo_pgsql` `pdo` `curl` `mbstring` `json`
- 可选：`swoole`（不用也行，可跑 PHP-FPM）、`gd`（生成验证码）、`openssl`（支付验签）
- PostgreSQL **14+**（用了 `DISTINCT ON`、`ON CONFLICT`、部分索引等，**不支持 MySQL/SQLite**）

### 安装

```bash
git clone https://gitee.com/xiaosu4610/LTZY-API-PHP.git
cd LTZY-API-PHP
composer install          # 如果没有 vendor/ 也可以不装，代码自带手写 autoload

# 先建好数据库（安装器只建表，不建库）
createdb -U postgres ltzy

# 启动（或配 systemd / nginx 反代）
php bin/server.php
```

然后浏览器打开 `http://你的地址:3000/install`，按向导填四步：

1. **环境检查** —— 自动检测 PHP 版本、扩展、目录权限
2. **数据库** —— 主机 / 端口 / 库名 / 用户 / 密码（带「测试连接」）
3. **站点信息** —— 名称、地址、Logo
4. **管理员** —— 用户名与密码

点「开始安装」后自动执行迁移、写 `.env`、建管理员、上锁。

> **装完之后 `/install` 会永久 404**。想重装就删掉 `var/install.lock`，入口自动恢复。

重启服务让配置生效：

```bash
systemctl restart ltzy-api      # 或你用的进程管理器
```

### 生产部署（推荐 systemd + nginx）

```ini
# /etc/systemd/system/ltzy-api.service
[Unit]
Description=LTZY-API
After=network.target postgresql.service

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/ltzy
EnvironmentFile=/opt/ltzy/.env
ExecStart=/usr/bin/php /opt/ltzy/bin/server.php
Restart=always

[Install]
WantedBy=multi-user.target
```

nginx 反代注意三点：`proxy_buffering off`（否则 SSE 不流）、`proxy_read_timeout` 要大于 `RELAY_TIMEOUT`、`client_max_body_size` 按需放大（音频上传）。

---

## 目录结构

```
.
├── app/                核心代码
│   ├── Billing/        计费
│   ├── Db/             数据库连接池
│   ├── Domain/         领域层（用户 / 渠道 / 账户）
│   ├── Http/           HTTP 层（路由 / 中间件 / 控制器）
│   ├── Install/        安装器
│   ├── Payment/        支付
│   ├── Protocol/       四协议互转
│   ├── Relay/          中继（转发 / 密钥池 / 各服务）
│   ├── Security/       限流 / 验证码 / 登录保护
│   └── Support/        基础工具（日志 / 上下文 / env / JSON）
├── bin/                CLI 脚本（启动 / 迁移 / 诊断 / 运维）
├── config/             配置（读环境变量）
├── deploy/             部署脚本
├── migrations/         SQL 迁移（按序号执行）
├── web/                前端（纯静态单文件，零构建）
└── var/                运行时（日志、安装锁）
```

**前端是纯静态 HTML**，内嵌 CSS/JS，没有 npm、没有构建步骤。改完刷新即生效。

---

## 常用命令

```bash
php bin/migrate.php            # 执行未应用的迁移
php bin/migrate.php --status   # 查看迁移状态
php bin/make-admin.php         # 创建管理员
php bin/set-admin.php          # 重置管理员密码
php bin/regression.php         # 回归测试
php bin/bench.php              # 压测
```

---

## 文档

> 本项目的内部工程文档（架构设计、部署细节、安全评估、踩坑记录）**不在本仓库**。
> 如需了解实现思路，请阅读源码注释——本项目坚持「注释解释为什么，不是解释做什么」。

---

## 贡献

欢迎提交 Issue 和 Pull Request。请先读 [CONTRIBUTING.md](CONTRIBUTING.md)。

- 发现安全漏洞请**不要**公开 Issue，见 [SECURITY.md](SECURITY.md)
- 提交前请确保 `php -l` 全部通过、`php bin/regression.php` 无失败项

---

## 许可证

[MIT](LICENSE)
