# 贡献指南

感谢你愿意参与 LTZY-API。本文档说明**怎么提 Issue、怎么写代码、怎么发 PR、怎么发版**，以及在这个项目里协作的基本约定。

请先花 5 分钟读完——能省掉后面来回沟通的时间。

---

## 1. 行为准则

我们想要的是一个**能好好讨论技术**的地方。

**鼓励：**
- 对代码、设计、方案提具体意见，指出问题在哪、为什么、怎么改
- 不同意就直说不同意，并给出理由
- 承认自己不确定，问"这块我不熟，能解释一下吗"
- 指出别人的错误时，对事不对人

**不接受：**
- 人身攻击、嘲讽、贬低他人能力
- 针对性别、地域、民族、宗教、性取向、残障的歧视性言论
- 骚扰、威胁、持续的人身纠缠
- 在 Issue / PR 里发广告、刷屏、带货

**处理方式：** 维护者会先私下提醒；屡次发生则关闭评论权限或移出仓库。不接受就离开，不需要争论"我有没有违反"。

---

## 2. 分支模型

```
main ─────●─────────────●─────────────●────→  永远可部署
           \           / \           /
            \         /   \         /
feature/*    ●───●───●     ●───●───●          功能开发
                ↓
develop ────●───────●───────────●──────→      集成测试
                ↓
hotfix/*         ●──●                          紧急修复（直接从 main 拉）
```

### 分支说明

| 分支 | 用途 | 谁能推 | 保护 |
| --- | --- | --- | --- |
| `main` | **生产分支**，永远可部署 | 仅维护者（通过 PR 合并） | ✅ 禁止直接 push |
| `develop` | 集成测试分支，功能汇总处 | 维护者 | ✅ 禁止直接 push |
| `feature/<简短描述>` | 新功能 | 任何人 | ❌ 无 |
| `fix/<简短描述>` | 普通缺陷修复 | 任何人 | ❌ 无 |
| `hotfix/<版本>` | 线上紧急问题 | 维护者 | ❌ 无 |
| `docs/<简短描述>` | 纯文档改动 | 任何人 | ❌ 无 |

### 分支命名示例

```
feature/model-pricing-tiers
fix/relay-timeout-not-applied
hotfix/1.2.1
docs/contributing-guide
```

**用连字符，不用下划线或斜杠嵌套。** `feature/foo/bar` 这种嵌套会让人分不清哪个是分支名。

---

## 3. 提交信息规范

采用 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/)，格式：

```
<type>(<scope>): <描述>

[可选的正文]

[可选的脚注]
```

### type（必填）

| type | 含义 | 例子 |
| --- | --- | --- |
| `feat` | 新功能 | `feat(billing): 支持按 token 计费` |
| `fix` | 缺陷修复 | `fix(relay): 修复流式响应首字节超时未触发重试` |
| `perf` | 性能优化 | `perf(db): 连接池复用减少 53ms 建连开销` |
| `refactor` | 重构（不改行为） | `refactor(router): 拆出安装状态闸门` |
| `docs` | 文档 | `docs(readme): 补充 nginx SSE 配置说明` |
| `test` | 测试 | `test(security): 补充验证码重放用例` |
| `chore` | 杂项（依赖、构建、CI） | `chore(deps): 升级 swoole 到 6.2` |
| `style` | 格式（不影响逻辑） | `style: 统一缩进为 4 空格` |
| `build` | 构建系统 | `build: 增加 composer autoload 生成` |
| `ci` | CI 配置 | `ci: 加 php -l 检查` |
| `revert` | 回滚 | `revert: 回滚 #42` |

### scope（可选但推荐）

用**模块名**，不超 10 个字符：

`billing` `relay` `router` `auth` `db` `install` `payment` `protocol` `web` `security`

### 描述（必填）

- 用**中文或英文都行**，但一个仓库内保持一致
- 现在时、祈使句：「修复」而不是「修复了」
- 不超过 50 个字符
- **不要写「update」「fix bug」这种没有信息量的**

### 正文（可选）

**重点写「为什么」，不是「改了什么」。** `git diff` 已经告诉了改了什么。

```
fix(relay): 移除不支持的 curl 选项

Swoole 的 curl hook 不支持 CURLOPT_LOW_SPEED_LIMIT/TIME，
设置后会抛 "option[19] is not supported"，导致所有转发 500。

改为只靠 CURLOPT_TIMEOUT 控制超时，慢速检测交给上游自身。
```

### 关联 Issue

```
Closes #42
Fixes #17
Refs #8
```

---

## 4. 代码规范

### 4.1 通用

- **PHP 8.2+ 语法**：用 `declare(strict_types=1)`、类型声明、`match`、只读属性
- **4 空格缩进**，不用 Tab
- **行宽 110 字符**，超出就换行
- **文件末尾留一个空行**，不留多余空白行
- 文件名 `PascalCase.php`，与类名一致

### 4.2 命名

| 类型 | 风格 | 例子 |
| --- | --- | --- |
| 类 | PascalCase | `RelayService` |
| 方法 | camelCase | `resolveChannel` |
| 变量 | camelCase | `$priceCache` |
| 常量 | UPPER_SNAKE | `DEFAULT_MODEL_LIST` |
| 数据库表/字段 | snake_case | `model_prices.per_request_price` |

### 4.3 注释

**这个项目对注释有要求：注释解释「为什么」，不是「做什么」。**

```php
// ❌ 不要这样（废话）
// 遍历数组
foreach ($items as $it) { ... }

// ✅ 要这样（解释背后的坑）
// 这里必须用 strict 比较。松散比较下 "0" == 0 为 true，
// 价格字段是字符串时会导致免费模型被当成收费模型。
if ($price === 0) { ... }
```

**凡是「看起来可以更简单，但其实不能」的地方，必须写清原因。** 否则三个月后一定会有人"优化"掉它，然后线上出事。

### 4.4 错误处理

- 不要空 `catch {}`，至少 `Logger::warn()`
- 对外错误信息**不能泄漏内部细节**（SQL、文件路径、堆栈）
- 用户能看懂、能操作：「余额不足，去充值」而不是「quota exhausted」

### 4.5 安全底线（硬性要求）

- **SQL 一律用参数绑定**，禁止字符串拼接
- **用户输入的路径**必须校验（防路径穿越）
- **输出到 HTML** 必须转义（前端统一用 `esc()`）
- **不提交任何密钥**：`.env`、`.secrets/`、硬编码的 `sk-`
- **授权检查在服务端**，不信任任何前端传来的 `user_id`

---

## 5. Issue 规范

### 提 Issue 前

1. 搜索一下有没有重复的
2. 确认是**本项目的问题**而不是你的客户端配置问题
3. 准备好复现步骤

### Bug Report 要包含

```markdown
## 环境
- OS / PHP 版本 / Swoole 版本 / PostgreSQL 版本
- 部署方式（Swoole 直跑 / systemd / FPM）

## 复现步骤
1. 调 POST /v1/chat/completions ...
2. 带上 model=xxx
3. 观察返回

## 期望结果
200 + 正常回复

## 实际结果
500 + {"error":{"message":"..."}}

## 日志（脱敏后）
```

**日志请务必脱敏**：去掉 API Key、用户信息、真实域名。

### Feature Request 要包含

- **你要解决什么问题**（不是"我要一个 xxx 功能"）
- 现在的做法是什么、哪里不方便
- 你希望的方案，以及考虑过的替代方案

---

## 6. Pull Request 流程

### 6.1 开 PR 前

```bash
# 1. 从最新的 develop 拉分支
git checkout develop
git pull origin develop
git checkout -b feature/your-feature

# 2. 写代码

# 3. 自检（必须全过）
php -l <改动的每个文件>
php bin/regression.php          # 回归测试
php bin/security-test.php       # 安全测试（如改动涉及安全面）

# 4. 提交
git add -A
git commit -m "feat(billing): 支持按 token 计费"

# 5. 推
git push origin feature/your-feature
```

### 6.2 PR 描述模板

PR 会自动带出模板（见 `.github/pull_request_template.md`），请填完整：

- **改动原因**：解决什么问题
- **改动内容**：做了什么
- **测试方式**：怎么验证的
- **影响面**：会不会影响现有功能
- **截图**（涉及 UI 时）

### 6.3 Review 标准

维护者会按以下清单审：

| 检查项 | 说明 |
| --- | --- |
| **能跑** | 自己跑过吗？有没有复现步骤？ |
| **测试** | `regression.php` 有没有新失败？有新增用例吗？ |
| **安全** | 有 SQL 注入 / XSS / 越权 / 密钥泄漏风险吗？ |
| **性能** | 有没有在热路径上做全表扫描 / 同步 IO？ |
| **可读性** | 三个月后别人能看懂吗？复杂逻辑有注释吗？ |
| **范围** | 这个 PR 只做一件事吗？（混进无关改动会被要求拆分） |
| **破坏性** | 有没有改 API 契约、数据库结构、配置项？有的话要说明迁移方案 |

### 6.4 合并规则

- **必须至少一位维护者 Approve**
- **CI 通过**（如果配了）
- **合并方式**：
  - 功能分支 → `develop`：**Squash merge**（保持 develop 历史干净）
  - `develop` → `main`（发版）：**Merge commit**（保留完整历史）
- **合并后**：删除已合并的远程分支

### 6.5 会被直接关闭的 PR

- 只改格式/空行的纯噪音 PR
- 一个 PR 里混了 5 个不相关的功能
- 没有描述、看不出目的
- 引入新依赖却不说为什么
- 提交信息全是 "update" "fix"
- 明显是 AI 生成但没经过人工验证代码（跑都没跑过）

---

## 7. 发布流程

### 7.1 版本号

遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)：`MAJOR.MINOR.PATCH`

| 变更类型 | 版本位 | 例子 |
| --- | --- | --- |
| 不兼容的 API 改动 | MAJOR | `1.0.0 → 2.0.0` |
| 向下兼容的新功能 | MINOR | `1.0.0 → 1.1.0` |
| 向下兼容的缺陷修复 | PATCH | `1.0.0 → 1.0.1` |

### 7.2 发版步骤

```bash
# 1. 从 develop 拉 release 分支
git checkout -b release/1.1.0 develop

# 2. 更新版本号与 CHANGELOG
#    - CHANGELOG.md 加新版本段落
#    - app/ 或 config/ 里的版本常量（如有）

# 3. 提交
git commit -m "chore(release): 准备 1.1.0"

# 4. 合并到 main
git checkout main
git merge --no-ff release/1.1.0
git tag -a v1.1.0 -m "1.1.0"

# 5. 同步回 develop
git checkout develop
git merge --no-ff main

# 6. 推
git push origin main develop --tags
```

### 7.3 CHANGELOG 格式

```markdown
## [1.1.0] - 2026-11-15

### 新增
- 按 token 计费模式（#42）
- 安装向导

### 修复
- 流式响应首字节超时未触发重试（#51）

### 变更
- 移除 `LEGACY_XXX` 环境变量支持

### 破坏性变更
- `/v1/models` 现在强制鉴权，无密钥返回 401
```

**分类固定用：新增 / 修复 / 变更 / 破坏性变更 / 安全。**

### 7.4 标签

- 版本标签：`v1.1.0`（带 `v` 前缀）
- 预发布：`v1.1.0-rc.1`、`v1.1.0-beta.1`
- 标签必须有注释：`git tag -a v1.1.0 -m "..."`

---

## 8. 本项目特有的坑（写代码前必读）

这几条是踩出来的，违反了一定会出事：

| # | 规则 | 违反后果 |
| --- | --- | --- |
| 1 | **改完 PHP 必须 `systemctl restart llm-gateway`** | `opcache.validate_timestamps=0`，不重启等于没改 |
| 2 | **不要在 Swoole 里用静态属性存请求状态** | 常驻进程会串号，A 用户看到 B 用户的余额 |
| 3 | **Swoole 的 curl 不支持 `CURLOPT_LOW_SPEED_LIMIT/TIME`** | 抛 `option[19] is not supported`，全部转发 500 |
| 4 | **常驻进程里的 `is_file()` 要配 `clearstatcache()`** | 删了文件仍返回 true，安装状态判断失效 |
| 5 | **协程内不做文件加载/JSON 大对象解析** | 触发无输出段错误，日志里什么都没有 |
| 6 | **对外错误信息不能暴露用户体系** | 说「需经销商权限」等于告诉攻击者「这站有经销商」 |

---

## 9. 沟通渠道

- **Bug / 功能建议**：提 Issue
- **用法问题**：先看 README 和 Issue 历史
- **安全漏洞**：见 [SECURITY.md](SECURITY.md)，**不要公开 Issue**
- **需要讨论设计**：开 Discussion 或 Issue 打 `discussion` 标签

---

## 10. 许可

提交 PR 即表示你同意你的代码以本项目的 [MIT](LICENSE) 许可证发布。
