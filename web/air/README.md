> ## ⚠️ 该主题已冻结（LTZY-API 二开）
>
> 自 LTZY-API 二开起，本主题（`web/air`）**不再同步新功能**。原因：
> 1. 没有 `node_modules` 与锁文件，**改完无法构建验证**；
> 2. 没有 i18n 机制（约 738 处硬编码中文 / 35 个文件），单独建一套词条属沉没成本；
> 3. 缺少运营看板 `/operations` 与 i18n 语言切换。
>
> 现在唯一主线主题是 **`web/default`**（功能最全、唯一有 i18n、唯一可构建验证）。
> 令牌的接口 scope（C3）已在 E8 补齐；如需恢复本主题请看
> [`web/E8-THEME-CONVERGENCE.md`](../E8-THEME-CONVERGENCE.md)。
>
> 以下为上游原始说明。
# React Template

## Basic Usages

```shell
# Runs the app in the development mode
npm start

# Builds the app for production to the `build` folder
npm run build
```

If you want to change the default server, please set `REACT_APP_SERVER` environment variables before build,
for example: `REACT_APP_SERVER=http://your.domain.com`.

Before you start editing, make sure your `Actions on Save` options have `Optimize imports` & `Run Prettier` enabled.

## Reference

1. https://github.com/OIerDb-ng/OIerDb
2. https://github.com/cornflourblue/react-hooks-redux-registration-login-example