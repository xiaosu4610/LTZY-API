# LTZY-API 前端 i18n 键名规范与命名空间约定

> 版本：v1（E7 前端 i18n 统一 交付）
> 适用对象：`web/` 下所有主题（当前 `default`、`air`、`berry`），以及后续自研统一主题。
> 配套工具：`web/scripts/check-i18n.js`

---

## 0. 一句话规则

**界面文案一律走词条；词条键一律是「命名空间.区域.字段」的小写 snake_case 路径；新增词条必须 zh / en 同时补齐；提交前跑一次 `node web/scripts/check-i18n.js`。**

---

## 1. 技术栈与目录约定（每个主题一套）

```
web/<theme>/
  src/
    i18n.js                      # i18next 初始化，集中注册语言资源
    locales/
      zh/translation.json        # 中文词条（基准语言，fallbackLng）
      en/translation.json        # 英文词条
    App.js                       # import './i18n' 触发初始化
```

约定：

- **不使用命名空间（namespace）文件拆分**，所有词条放在单一 `translation` 命名空间下的 `translation.json` 里。
  理由：当前体量（default 约 640 条）用顶层键分层已经足够，多 namespace 会带来 `t('ns:key')` 写法与加载器的额外复杂度，收益为零。
- `fallbackLng` 固定为 `zh`：中文是产品默认语言，缺词条时回退中文比回退英文对现有用户更友好。
- 新增语言时只需在 `locales/` 下加目录 + 在 `i18n.js` 的 `resources` 注册，并在 Header 的语言切换器里加一项。
- **词条文件只放文案，不放业务数据。** 模型名、渠道 ID、用户名、日志原文、后端返回的 `message` 一律不进词条。

---

## 2. 命名空间（顶层键）划分

顶层键即命名空间，按「界面区域」划分，不按「组件」划分（组件会重构，界面区域稳定）。

| 命名空间 | 用途 | 典型键 |
| --- | --- | --- |
| `header` | 顶部导航、注销/登录入口 | `header.token`、`header.operations` |
| `common` | 跨页面复用的格式与单位 | `common.quota.display`、`common.quota.unit` |
| `messages` | 全局提示（成功 / 失败 / 通知） | `messages.success.login`、`messages.error.network` |
| `auth` | 登录 / 注册 / 重置密码 | `auth.login.title`、`auth.register.get_code` |
| `home` | 首页、系统状态 | `home.welcome.title`、`home.system_status.config.title` |
| `dashboard` | 总览页图表 | `dashboard.charts.requests.title` |
| `operations` | 运营看板（成本 + 渠道健康度） | `operations.summary.total_quota`、`operations.table.requests` |
| `channel` | 渠道管理 | `channel.edit.title_edit`、`channel.buttons.test_all` |
| `token` | 令牌管理 | `token.edit.rpm`、`token.edit.modes.chat` |
| `redemption` | 兑换码管理 | `redemption.table.quota` |
| `log` | 日志页 | `log.table.prompt_tokens` |
| `user` | 用户管理 | `user.table.role_types.admin` |
| `setting` | 系统设置（四个 Tab） | `setting.operation.quota.new_user` |
| `topup` | 充值中心 | `topup.redeem_code.submit` |
| `about` | 关于页 | `about.description` |
| `footer` | 页脚版权 | `footer.license` |

**新增命名空间的准入条件**：出现一个全新的、有独立路由的界面区域。一个页面内部的细分区域用**键的第二段**表达（如 `operations.summary`、`operations.trend`），不要为它新开顶层键。

---

## 3. 键名命名规则

### 3.1 字符与结构

- 全小写，单词间用 `_`，层级间用 `.`。例：`token.edit.daily_quota_limit_placeholder`。
- 层级深度**不超过 5 段**（顶层命名空间 + 最多 4 层），超过说明拆分过细，应上提一层。
- 禁止大写、驼峰、连字符、空格：`token.edit.dailyQuotaLimit` ❌、`token.edit.daily-quota` ❌。
- 禁止在键里出现数字以外的业务值，禁止把循环下标写进键。

### 3.2 段位语义（同一区域的固定顺序）

`<命名空间>.<区域>.<字段>[_限定词]`

| 段位 | 含义 | 示例 |
| --- | --- | --- |
| 第 1 段 | 命名空间 | `token` |
| 第 2 段 | 区域 / 子页面 | `edit`、`table`、`buttons`、`messages`、`status`、`range`、`summary` |
| 第 3 段起 | 字段名，与代码里的字段名 / 语义保持一致 | `daily_quota_limit` |

### 3.3 保留的字段名后缀与容器键

这些第二段名是**保留容器**，含义固定，不要挪作他用：

| 容器 | 放什么 | 例 |
| --- | --- | --- |
| `title` / `title_edit` / `title_create` | 页面或卡片标题；新增/编辑共用页面用后两者区分 | `token.edit.title_edit` |
| `table.*` | 表格列头、表内固定文案 | `operations.table.success_rate` |
| `buttons.*` | 按钮文案 | `channel.edit.buttons.submit` |
| `messages.*` | 该区域的 toast / 提示 | `token.edit.messages.update_success` |
| `status.*` | 状态枚举文案 | `operations.status.auto_disabled` |
| `range.*` | 时间窗口选择项 | `operations.range.hours_24` |
| `modes.*` / `role_types.*` / `type.*` | 枚举值到文案的映射 | `token.edit.modes.embeddings` |
| `*_placeholder` | 输入框 placeholder | `token.edit.rpm_placeholder` |
| `*_notice` | 提示条 / Banner | `token.edit.quota_notice` |

### 3.4 枚举键的命名

枚举键名**必须逐字等于后端的枚举取值**（小写化），不要翻译成英文单词或另起别名：

```json
"modes": { "audio_transcription": "语音识别" }   // 对应后端 relaymode 值 audio_transcription ✅
"modes": { "speech_to_text": "语音识别" }        // 自造别名 ❌
```

理由：后端枚举新增时，靠键名就能一眼定位缺失词条，也让 `check-i18n.js` 的差集报告可读。

### 3.5 插值与复数

- 插值统一用 `{{name}}` 双花括号，参数名用 snake_case：`"{{count}} 个令牌创建成功"`。
- **不使用 i18next 的复数后缀**（`_one` / `_other`）：中文无复数变化，引入后中文词条会被迫写两份完全相同的文案。
  需要区分时用显式键或文案内插值表达。
- 不要在词条里拼 HTML。需要富文本时在组件里拆成多个词条（现有 `setting.system.github.url_notice` 那种做法）。

---

## 4. 什么**不**进词条

| 内容 | 处理方式 | 理由 |
| --- | --- | --- |
| 接口路径、协议常量（`/v1/chat/completions`） | 保留字面量，与词条拼接 | 机器可读，翻译会误导 |
| 模型名、渠道名、用户名、令牌名 | 原样渲染 | 用户数据 |
| 后端返回的 `message` | `showError(message)` 原样展示 | 后端负责本地化；前端只兜底 fallback |
| 日志、公告、首页内容 | 后端存储的用户内容 | 不是词条 |
| 纯开发者注释 | 中文注释（本项目规范） | 不进产物，不面向用户 |

前端**兜底 fallback** 例外：当后端 message 为空时展示的前端兜底文案（如 `token.edit.messages.load_failed`）必须走词条。

---

## 5. 组件内使用规范

### 5.1 基本写法

```js
import { useTranslation } from 'react-i18next';

const Page = () => {
  const { t } = useTranslation();
  return <Button>{t('token.edit.buttons.submit')}</Button>;
};
```

### 5.2 模块级常量不能调用 `t`

`t` 只在组件渲染期可用，模块级数组/对象里**禁止**写死已翻译的字符串。统一改为**存键、渲染时翻译**：

```js
// ✅ 正确：常量只存 key，文案在渲染期解析
const HOUR_OPTIONS = [
  { key: 1, labelKey: 'operations.range.hours_1', value: 1 },
];

options={HOUR_OPTIONS.map((o) => ({ key: o.key, value: o.value, text: t(o.labelKey) }))}

// ❌ 错误：模块级写死文案，语言切换后不生效
const HOUR_OPTIONS = [{ key: 1, text: '最近 1 小时', value: 1 }];
```

### 5.3 动态键的硬约束

只允许 `t(obj.labelKey)`、`t(status.labelKey)` 这类**对象属性取值**形式，并且：

- 该属性的所有可能取值必须**以字符串字面量**出现在同一个文件里（例如 `labelKey: 'token.edit.modes.chat'`）；
- **禁止** `t('a.b.' + x)`、`t(\`a.b.${x}\`)` 这类拼接：它会绕过 `check-i18n.js` 的静态扫描，产生无法被自动发现的缺词条风险。

`check-i18n.js` 对出现动态 `t(...)` 的文件会退化为「收集该文件内所有形如词条键的字符串字面量」，因此上面的约束同时也是让检查保持准确的前提。

### 5.4 语言相关的格式化必须跟随 `i18n.language`

数字千分位、日期格式不要写死 `'zh-CN'`：

```js
const { t, i18n } = useTranslation();
const locale = (i18n.language || 'zh').startsWith('en') ? 'en-US' : 'zh-CN';
const formatNumber = (value, locale) => Number(value || 0).toLocaleString(locale);
```

---

## 6. 新增词条的标准流程

1. 先在 `locales/zh/translation.json` 按第 2、3 节规范挑好键位（**不要另起同义键**，先搜索是否已有可复用词条）。
2. **同一次改动内**补齐 `locales/en/translation.json` 的同一个键。缺 en 会导致英文界面显示原始键名，属于线上事故级问题。
3. 组件里用 `t('...')` 引用。
4. 运行 `node web/scripts/check-i18n.js`，确认：
   - `[缺失键]` 为 0；
   - `[单语言缺失]` 为 0；
   - 自己新增的键没有落进 `[无引用键]`（说明拼错了键名或忘了引用）。
5. 构建对应主题（`web/default` 见第 8 节命令）。

---

## 7. 键一致性检查（`web/scripts/check-i18n.js`）

```bash
node scripts/check-i18n.js              # 自动发现 web/ 下所有含 src/locales 的主题
node scripts/check-i18n.js default      # 只检查 default
node scripts/check-i18n.js default air  # 指定多个主题
node scripts/check-i18n.js --strict     # 无引用键 / 跨语言漂移也视为失败
```

脚本做三件事，输出含义与处置：

| 检查项 | 含义 | 默认是否失败 | 处置 |
| --- | --- | --- | --- |
| `[缺失键]` | 源码 `t('x')` 引用了、但**任何**语言都没有的键 | ✅ 失败（exit 1） | 补词条，或修正键名拼写 |
| `[单语言缺失]` | 某语言缺该键，其他语言有 | ✅ 失败 | 补齐该语言；用户会直接看到原始键名 |
| `[跨语言不一致]` | 某语言多出 / 少掉未被引用的历史词条 | ❌ 仅告警 | 清理僵尸词条（`--strict` 下失败） |
| `[无引用键]` | 词条存在但源码无静态引用 | ❌ 仅告警 | 删除，或确认是动态键但没按 5.3 写法落字面量 |

**为什么放在 `web/scripts/` 而不是某个主题的 `src/` 下**：`src/` 内的文件属于构建输入，容易在后续被误 import 进产物；这是纯开发期静态检查工具，必须留在构建目录之外。

**为什么检查脚本零依赖**：三个主题各自装一套 `node_modules`（且 `air` / `berry` 当前根本没装），工具若依赖第三方包就没法在任意主题目录下直接跑起来。

CI 集成建议：在 Lint 阶段执行 `node web/scripts/check-i18n.js`，先跑默认模式（只卡缺失键，不卡历史遗留），等僵尸词条清理干净后再切 `--strict`。

---

## 8. 构建与验证约定

- 项目路径含中文，Node 会解析乱码，**必须先 `subst X: "E:\代码2"`**，再在 `X:` 盘下操作。
- 构建命令用显式入口，不要用 `npm run build`（各主题 `package.json` 的 build 脚本里是 `rm -rf` / `mv` 这类 Unix 命令，Windows 上会失败）：

```powershell
cd X:\src\web\default
node node_modules\react-scripts\bin\react-scripts.js build
```

- `web/build/**` 是构建产物，由负责人统一搬运，**不要**在功能改动里手改。

---

## 9. 主题演进约定（给 air / berry / 自研主题）

新主题接入 i18n 的**最低要求**：

1. 依赖 `i18next` + `react-i18next` + `i18next-browser-languagedetector`（版本与 `web/default` 对齐）。
2. 目录结构、`fallbackLng: 'zh'`、单一 `translation` 命名空间与第 1 节一致。
3. **顶层键沿用第 2 节的命名空间表**，不得自创同义命名空间。同一功能在 `default` 与自研主题里必须用**同一个键**——这是「统一主题」能一键切换语言、词条可互相搬运的前提。
4. 允许自研主题存在第 2 节未覆盖的**新界面区域**，此时按准入条件新开顶层键，但要同步把该命名空间登记进本文件。
5. 接入后 `node web/scripts/check-i18n.js <theme>` 必须为 exit 0。

主题间的词条搬运原则：**键名是契约，文案是实现**。跨主题复用键名，但允许各主题的文案措辞按自己的设计语言微调。

---

## 10. 正例 / 反例速查

```jsonc
// ✅ 正例：层级清晰、枚举对齐后端、zh/en 同构
"token": {
  "edit": {
    "daily_quota_limit": "每日预算上限（额度，0 表示不限）",
    "daily_quota_limit_placeholder": "例如 500000（约 1 美元）",
    "modes": { "audio_transcription": "语音识别" },
    "messages": { "load_failed": "令牌信息加载失败！" }
  }
}
```

```jsonc
// ❌ 反例
"Token_Edit_DailyQuota": "...",          // 命名空间未小写、驼峰、层级压平
"token.edit.新增预算上限": "...",         // 键里混入中文
"common.tip1": "例如 60",                // 无语义的编号键
"token.edit.daily_quota_limit2": "...",  // 用数字后缀迭代同义键
"modes": { "speech_to_text": "语音识别" } // 枚举名与后端不一致
```
