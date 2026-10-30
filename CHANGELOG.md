# Changelog

本项目的所有重要变更都会记录在这里。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

分类固定用：**新增 / 修复 / 变更 / 破坏性变更 / 安全**。

---

## [Unreleased]

### 新增
- 安装向导 `/install`：浏览器内 4 步完成初始化（环境检查 → 数据库 → 站点信息 → 管理员），装完入口自动 404
- `.env` 加载器（`app/Support/Env.php`），环境变量优先级高于文件
- `Response::redirect()`，用 302 而非 301（避免浏览器永久缓存）
- `SECURITY.md` / `CONTRIBUTING.md` / Issue 与 PR 模板

### 修复
- `Request::origin()` 的 Host 白名单不再硬编码项目域名，改为从 `SITE_URL` / `SITE_ALLOWED_HOSTS` 推导
- `Billing` 在未配置 `SITE_URL` 时不再拼出 `/login` 半截链接
- Swoole 常驻进程下 `is_file()` 的 stat cache 导致安装状态判断失效（加 `clearstatcache`）

### 变更
- 超级管理员默认用户名 `root`，密码 `123456`（**部署后必须改**）
- 项目目录扁平化：`v2/` 的子目录提升为仓库根
- `relay/`（接口机中继，含上游密钥明文）不纳入开源仓库

---

## [0.1.0] - 2026-10-29

首个可用版本。

### 新增

**协议与中继**
- OpenAI Chat Completions / OpenAI Responses / Anthropic Messages / Gemini 四协议互转
- SSE 流式透传，逐块转发不缓冲
- 上游响应字节透传，只允许改写 `model` 字段
- 300 秒硬超时 + 3 次重试（重试前检查是否已推送内容）

**渠道与密钥**
- 多渠道路由（按模型→渠道映射，支持优先级与权重）
- 密钥池轮询，401/403/429 分别单独冷却
- 渠道端点能力标记（chat / responses / embeddings / rerankings / images / audio）

**计费**
- 按次计费，免费 / 收费双轨
- 每人独立定价（`account_models`），无专属价时回落全局定价
- 1 元 = 1,000,000 quota
- 分组倍率

**用户体系**
- 注册 / 登录 / 令牌管理
- 验证码、登录失败锁定、注册限流
- 在线充值（易支付 epay 协议），异步回调入账，幂等防重
- 调用日志与用量统计

**前端**
- 门户首页、模型广场、控制台（纯静态单文件，零构建）
- 在线试玩（Playground）
- 深色 / 浅色自适应

**运维**
- SQL 迁移（`bin/migrate.php`，按序号幂等执行）
- 回归测试（`bin/regression.php`）与安全测试（`bin/security-test.php`）
- 压测脚本（`bin/bench.php`）

### 安全

- SQL 全部参数绑定
- 密码 bcrypt 存储，登录用常量时间比较
- 验证码一次性消费 + 过期
- 响应头：`nosniff` / `X-Frame-Options: DENY` / `Referrer-Policy` / CSP
- 公开错误信息不泄漏用户体系与内部实现
- Host 头校验，防止 base_url 钓鱼
- 限流：验证码 12/分，充值 5/分 + 20/时 + 待支付 ≤3

---

## 链接

- [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)
- [Semantic Versioning](https://semver.org/lang/zh-CN/)
