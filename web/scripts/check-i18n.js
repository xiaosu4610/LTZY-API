#!/usr/bin/env node
/**
 * check-i18n.js —— 前端 i18n 键一致性检查脚本（E7 交付物）
 *
 * 为什么放在 web/scripts/ 而不是某个主题的 src/ 下：
 *   src/ 下的任何文件都会被 CRA 打进构建产物（哪怕没有被 import，也仍属于源码目录，
 *   容易在后续被误引用），而这是纯开发期的静态检查工具，不应该进入产物。
 *
 * 它做三件事：
 *   1) 跨语言对齐检查：同一个主题下 locales/zh 与 locales/en 的键集合必须一致，
 *      否则运行时会 fallback 到另一种语言，界面出现「中英混排」。
 *   2) 缺失键检查（硬失败）：src/**\/*.js 里 t('xxx') 引用但词条文件里没有的键。
 *      这是最常见的线上事故：页面上直接显示出原始键名 token.edit.rpm。
 *   3) 无引用键检查（警告）：词条文件里定义了但代码中没有任何 t('xxx') 引用的键。
 *      它本身不影响运行，但会随功能迭代不断积累成「僵尸词条」，
 *      是判断某个主题是否落后的量化指标（E8 / 自研主题据此对齐）。
 *
 * 用法：
 *   node scripts/check-i18n.js              # 自动发现 web/ 下所有含 src/locales 的主题
 *   node scripts/check-i18n.js default      # 只检查 web/default
 *   node scripts/check-i18n.js default air  # 指定多个主题
 *   node scripts/check-i18n.js --strict     # 无引用键也视为失败（CI 收紧时用）
 *
 * 退出码：缺失键 / 跨语言不一致 / --strict 下的无引用键 => 1，否则 0。
 */

'use strict';

const fs = require('fs');
const path = require('path');

// web/ 目录：本文件位于 web/scripts/ 下，所以上一级就是 web/
const WEB_DIR = path.resolve(__dirname, '..');

const argv = process.argv.slice(2);
const STRICT = argv.includes('--strict');
const themeArgs = argv.filter((a) => !a.startsWith('--'));

/**
 * 递归收集目录下的文件（按扩展名过滤）。
 * 为什么手写而不用 glob 依赖：web/ 下每个主题的 node_modules 各装一套依赖，
 * 检查脚本必须是零依赖的，才能在任何主题目录下直接 node 运行。
 */
function walk(dir, exts, out = []) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch (e) {
    return out;
  }
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      // 跳过构建产物与依赖目录，避免扫到压缩后的代码造成海量误报
      if (entry.name === 'node_modules' || entry.name === 'build' || entry.name === 'dist') continue;
      walk(full, exts, out);
    } else if (exts.some((ext) => entry.name.endsWith(ext))) {
      out.push(full);
    }
  }
  return out;
}

/** 自动发现主题：含 src/locales 子目录的才是走 i18next 的主题 */
function discoverThemes() {
  return fs
    .readdirSync(WEB_DIR, { withFileTypes: true })
    .filter((e) => e.isDirectory() && e.name !== 'scripts' && e.name !== 'build')
    .map((e) => e.name)
    .filter((name) => fs.existsSync(path.join(WEB_DIR, name, 'src', 'locales')));
}

/** 把嵌套 JSON 打平成 { 'a.b.c': value }，数组按下标打平（与 i18next 的取值语义一致） */
function flatten(obj, prefix = '', out = {}) {
  for (const [key, value] of Object.entries(obj)) {
    const full = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      flatten(value, full, out);
    } else {
      out[full] = value;
    }
  }
  return out;
}

/**
 * 从源码中提取 t('...') / t("...") / i18n.t('...') 的静态键。
 *
 * 用负向后行断言 (?<![\w$.]) 排除 print( / split( / format( 这类同名结尾调用，
 * 只允许行首或非标识符字符开头；同时兼容可选的 i18n. 前缀。
 * 模板字符串与键拼接一律跳过（本项目当前没有这种写法，出现时应人工确认）。
 *
 * 另外处理「动态键」：像 Header.js 里 `t(button.name)` 配 `name: 'header.token'`
 * 这种写法，静态扫不到引用，会把 header.* 整片误报成无引用键。
 * 因此对出现动态 t(...) 的文件，额外把其中所有「形如 i18n 键」的字符串字面量
 * 记为疑似引用（放进 heuristic 集合），既压掉误报也保留可解释性。
 */
function extractUsedKeys(files) {
  const used = new Map(); // key -> Set(相对路径:行号)
  const heuristic = new Set(); // 疑似被动态 t(变量) 引用的键
  const staticRe = /(?<![\w$.])(?:i18n\.)?t\(\s*(['"])((?:(?!\1)[^\\])+)\1/g;
  const anyCallRe = /(?<![\w$.])(?:i18n\.)?t\(/g;
  const keyShape = /^[a-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+$/;
  const stringLitRe = /(['"])((?:(?!\1)[^\\])+)\1/g;

  for (const file of files) {
    const content = fs.readFileSync(file, 'utf8');
    let match;
    while ((match = staticRe.exec(content)) !== null) {
      const key = match[2];
      // 跳过空串与疑似动态拼接残留（含空白或 ${ 的都不是合法静态键）
      if (!key.trim() || key.includes('${')) continue;
      const line = content.slice(0, match.index).split('\n').length;
      if (!used.has(key)) used.set(key, new Set());
      used.get(key).add(`${file}:${line}`);
    }

    // 判断该文件是否存在「第一个实参不是字符串字面量」的 t(...) 调用
    let hasDynamic = false;
    anyCallRe.lastIndex = 0;
    while ((match = anyCallRe.exec(content)) !== null) {
      const after = content.slice(match.index + match[0].length).replace(/^\s+/, '');
      if (after === '' || after[0] === ')' || after[0] === "'" || after[0] === '"') continue;
      hasDynamic = true;
      break;
    }
    if (hasDynamic) {
      stringLitRe.lastIndex = 0;
      while ((match = stringLitRe.exec(content)) !== null) {
        if (keyShape.test(match[2])) heuristic.add(match[2]);
      }
    }
  }
  return { used, heuristic };
}

let hasError = false;

function checkTheme(theme) {
  const themeDir = path.join(WEB_DIR, theme);
  const localesDir = path.join(themeDir, 'src', 'locales');
  const srcDir = path.join(themeDir, 'src');

  console.log(`\n==================== 主题：${theme} ====================`);

  // 主题尚未接入 i18next（air / berry 当前就是这种情况）：
  // 明确报「未接入」而不是抛异常，这样脚本可以直接用于盘点三套主题的接入状态。
  if (!fs.existsSync(localesDir)) {
    console.log('  [未接入 i18n] 缺少 src/locales，该主题没有 i18next 资源文件（本次跳过检查）。');
    if (STRICT) hasError = true; // --strict 表示「列出的主题都必须已接入」
    return { theme, notOnboarded: true, missingKeys: [], unusedKeys: [], driftKeys: [] };
  }

  const locales = fs
    .readdirSync(localesDir, { withFileTypes: true })
    .filter((e) => e.isDirectory() && fs.existsSync(path.join(localesDir, e.name, 'translation.json')))
    .map((e) => e.name)
    .sort();

  console.log(`语言：${locales.join(', ')}`);

  const localeKeys = {};
  for (const lang of locales) {
    const file = path.join(localesDir, lang, 'translation.json');
    let json;
    try {
      json = JSON.parse(fs.readFileSync(file, 'utf8'));
    } catch (e) {
      console.log(`  [错误] ${lang}/translation.json 不是合法 JSON：${e.message}`);
      hasError = true;
      continue;
    }
    localeKeys[lang] = flatten(json);
    console.log(`  ${lang}: ${Object.keys(localeKeys[lang]).length} 个词条 (${path.relative(WEB_DIR, file)})`);
  }

  const langs = Object.keys(localeKeys);
  const base = langs.includes('zh') ? 'zh' : langs[0];
  const allLocaleKeys = new Set();
  for (const lang of langs) Object.keys(localeKeys[lang]).forEach((k) => allLocaleKeys.add(k));

  // ---- 采集源码中使用到的键（放在最前，后面的判定都依赖它）----
  const sourceFiles = walk(srcDir, ['.js', '.jsx']);
  const { used, heuristic } = extractUsedKeys(sourceFiles);
  // 缺失键判定要把疑似动态引用也算进去，否则 t(变量) 指向的键会被误判为缺失
  const referenced = new Set([...used.keys(), ...heuristic]);
  console.log(
    `  扫描源码：${sourceFiles.length} 个文件，静态引用 ${used.size} 个键，疑似动态引用 ${heuristic.size} 个键`
  );

  // ---- 检查 1：缺失键（硬失败）----
  // 逐个语言分别判定，而不是只看并集：某个键只在 en 里存在、zh 里没有，
  // 并集判定会漏掉它，但中文用户界面上会直接显示原始键名。
  const missingKeys = [...referenced].filter((k) => !allLocaleKeys.has(k)).sort();
  const missingPerLang = {}; // lang -> [key]
  for (const lang of langs) {
    missingPerLang[lang] = [...referenced].filter((k) => !(k in localeKeys[lang])).sort();
  }
  if (missingKeys.length) {
    hasError = true;
    console.log(`  [缺失键] ${missingKeys.length} 个 —— 源码引用但任何词条文件都不存在：`);
    for (const key of missingKeys) {
      const refs = used.has(key)
        ? [...used.get(key)].slice(0, 3).map((p) => path.relative(WEB_DIR, p))
        : ['(疑似动态引用)'];
      console.log(`    - ${key}  <- ${refs.join(', ')}${used.has(key) && used.get(key).size > 3 ? ' ...' : ''}`);
    }
  } else {
    console.log('  [缺失键] 无');
  }
  for (const lang of langs) {
    const only = missingPerLang[lang].filter((k) => allLocaleKeys.has(k));
    if (only.length) {
      hasError = true;
      console.log(`  [单语言缺失] ${only.length} 个 —— ${lang} 缺少（其他语言已有，${lang} 用户会看到原始键名）：`);
      for (const key of only) console.log(`    - ${key}`);
    }
  }

  // ---- 检查 2：跨语言一致性（默认告警，--strict 下失败）----
  // 只统计「没有被任何语言完全覆盖」的词条；只影响回退语言显示，通常不致命，
  // 所以默认只告警，避免把历史遗留漂移变成阻断项。
  const driftKeys = [];
  if (base) {
    const baseKeys = new Set(Object.keys(localeKeys[base]));
    for (const lang of langs) {
      if (lang === base) continue;
      const other = new Set(Object.keys(localeKeys[lang]));
      for (const k of baseKeys) {
        if (!other.has(k) && !missingPerLang[lang]?.includes(k)) driftKeys.push(`${lang} 缺少 ${k}`);
      }
      for (const k of other) {
        if (!baseKeys.has(k)) driftKeys.push(`${lang} 多出 ${k}`);
      }
    }
  }
  if (driftKeys.length) {
    console.log(`  [跨语言不一致] ${driftKeys.length} 处（未引用的历史词条漂移，默认仅告警）：`);
    for (const d of driftKeys) console.log(`    - ${d}`);
    if (STRICT) hasError = true;
  } else {
    console.log('  [跨语言不一致] 无');
  }

  // ---- 检查 3：无引用键（警告 / --strict 下失败）----
  const unusedKeys = [...allLocaleKeys].filter((k) => !referenced.has(k)).sort();
  if (unusedKeys.length) {
    console.log(`  [无引用键] ${unusedKeys.length} 个 —— 词条存在但源码中无静态引用：`);
    for (const key of unusedKeys) console.log(`    - ${key}`);
    if (STRICT) hasError = true;
  } else {
    console.log('  [无引用键] 无');
  }

  return { theme, missingKeys, unusedKeys, driftKeys };
}

const themes = themeArgs.length ? themeArgs : discoverThemes();
if (!themes.length) {
  console.log('未发现任何含 src/locales 的主题。');
  process.exit(0);
}

const results = themes.map(checkTheme);

console.log('\n==================== 汇总 ====================');
for (const r of results) {
  if (r.notOnboarded) {
    console.log(`${r.theme.padEnd(10)} 未接入 i18n（无 src/locales）`);
    continue;
  }
  console.log(
    `${r.theme.padEnd(10)} 缺失键 ${String(r.missingKeys.length).padStart(3)}  跨语言不一致 ${String(
      r.driftKeys.length
    ).padStart(3)}  无引用键 ${String(r.unusedKeys.length).padStart(3)}`
  );
}
console.log(
  `模式：${STRICT ? '--strict（不一致 / 无引用键 / 未接入也视为失败）' : '默认（缺失键为失败，其余仅告警）'}`
);
process.exit(hasError ? 1 : 0);
