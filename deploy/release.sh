#!/usr/bin/env bash
#
# 代码上传后的发布脚本（在服务器上跑）
#
# 用法：
#   bash deploy/release.sh                 # 首次部署：建库表、装服务、启动、自检
#   bash deploy/release.sh --no-migrate    # 只重启，不碰数据库
#   bash deploy/release.sh --import /tmp/ltzy.sql   # 从 pg_dump 恢复数据
#
# 前提：deploy/provision.sh 已经跑过，PHP + OpenSwoole + PostgreSQL 就绪。

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/ltzy}"
DB_NAME="${DB_NAME:-ltzy}"
DB_USER="${DB_USER:-ltzy}"
APP_USER="${APP_USER:-ltzy}"
DO_MIGRATE=1
IMPORT_FILE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-migrate) DO_MIGRATE=0; shift ;;
        --import)     IMPORT_FILE="${2:-}"; shift 2 ;;
        *) echo "未知参数：$1"; exit 1 ;;
    esac
done

log()  { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()   { printf '  \033[1;32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[1;33m!\033[0m %s\n' "$*"; }
die()  { printf '\n\033[1;31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "需要 root 运行"

# ── 1. 运行用户与目录 ─────────────────────────────────────
log "1/7 运行用户与目录"
if ! id -u "${APP_USER}" >/dev/null 2>&1; then
    # 系统用户，不能登录，只用来跑服务
    useradd --system --no-create-home --shell /usr/sbin/nologin "${APP_USER}"
    ok "已建用户 ${APP_USER}"
else
    ok "用户 ${APP_USER} 已存在"
fi

[[ -d "${APP_DIR}" ]] || die "代码目录不存在：${APP_DIR}（先 rsync 上传）"
mkdir -p "${APP_DIR}/var"
chown -R "${APP_USER}:${APP_USER}" "${APP_DIR}/var"
chmod 750 "${APP_DIR}/var"

# 配置文件含数据库密码，只能属主可读
if [[ -f "${APP_DIR}/config/database.php" ]]; then
    chmod 600 "${APP_DIR}/config/database.php"
    ok "config/database.php 权限已收紧到 600"
else
    die "缺 ${APP_DIR}/config/database.php"
fi

if [[ -d "${APP_DIR}/.secrets" ]]; then
    chmod -R 600 "${APP_DIR}/.secrets" 2>/dev/null || true
    ok ".secrets 权限已收紧"
fi

# ── 2. 依赖检查 ───────────────────────────────────────────
log "2/7 运行依赖"
php -r 'exit(extension_loaded("swoole") ? 0 : 1);' || die "PHP 缺 swoole 扩展"
php -r 'exit(extension_loaded("pdo_pgsql") ? 0 : 1);' || die "PHP 缺 pdo_pgsql 扩展"
ok "swoole $(php -r 'echo SWOOLE_VERSION;') / PHP $(php -r 'echo PHP_VERSION;')"

php -r '
require $argv[1] . "/config/database.php";
$c = require $argv[1] . "/config/database.php";
try {
    $pdo = new PDO(sprintf("pgsql:host=%s;port=%d;dbname=%s", $c["host"], $c["port"], $c["database"]),
        $c["username"], $c["password"], [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
    echo "  ok\n";
} catch (Throwable $e) {
    fwrite(STDERR, "  连不上数据库：" . $e->getMessage() . "\n");
    exit(1);
}' "${APP_DIR}" || die "数据库连接失败，检查 config/database.php"

# ── 3. 数据导入（可选）────────────────────────────────────
if [[ -n "${IMPORT_FILE}" ]]; then
    log "3/7 导入数据：${IMPORT_FILE}"
    [[ -f "${IMPORT_FILE}" ]] || die "文件不存在：${IMPORT_FILE}"
    su - postgres -c "psql -d ${DB_NAME} -v ON_ERROR_STOP=1 -f '${IMPORT_FILE}'" > /tmp/import.log 2>&1 \
        || { tail -20 /tmp/import.log; die "导入失败，详见 /tmp/import.log"; }
    ok "导入完成：$(su - postgres -c "psql -d ${DB_NAME} -tAc 'SELECT count(*) FROM users'") 个账号"
else
    log "3/7 跳过数据导入（未指定 --import）"
fi

# ── 4. 迁移 ───────────────────────────────────────────────
if [[ "${DO_MIGRATE}" -eq 1 ]]; then
    log "4/7 应用数据库迁移"
    cd "${APP_DIR}"
    php bin/migrate.php || die "迁移失败"
else
    log "4/7 跳过迁移（--no-migrate）"
fi

# ── 5. systemd ────────────────────────────────────────────
log "5/7 systemd 服务"
cat > /etc/systemd/system/ltzy-api.service <<UNIT
[Unit]
Description=LTZY API v2 (PHP + OpenSwoole)
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User=${APP_USER}
Group=${APP_USER}
WorkingDirectory=${APP_DIR}
ExecStart=/usr/bin/php ${APP_DIR}/bin/server.php
Restart=always
RestartSec=3

# 常驻内存服务，给个上限防止失控把机器拖死
MemoryMax=1G
# 默认 1024 个 fd 在中等并发下就会 Too many open files
LimitNOFILE=65535

StandardOutput=append:${APP_DIR}/var/server.out.log
StandardError=append:${APP_DIR}/var/server.err.log

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable ltzy-api >/dev/null
systemctl restart ltzy-api
ok "服务已启动"

# ── 6. 等就绪 ─────────────────────────────────────────────
log "6/7 等待服务就绪"
READY=0
for i in $(seq 1 30); do
    sleep 1
    if curl -fsS --max-time 2 http://127.0.0.1:3000/api/status >/dev/null 2>&1; then
        READY=1
        ok "就绪（用时 ${i}s）"
        break
    fi
done
if [[ ${READY} -eq 0 ]]; then
    echo "--- 最近日志 ---"
    tail -30 "${APP_DIR}/var/server.out.log" 2>/dev/null || true
    tail -30 "${APP_DIR}/var/server.err.log" 2>/dev/null || true
    die "服务 30 秒内未就绪"
fi

# ── 7. 自检 ───────────────────────────────────────────────
log "7/7 自检"
cd "${APP_DIR}"

FAILED=0

echo "  · 技术栈头"
SERVER_HDR=$(curl -sI http://127.0.0.1:3000/ | tr -d '\r' | awk -F': ' 'tolower($1)=="server"{print $2}')
[[ "${SERVER_HDR}" == "nginx" ]] && ok "Server: nginx" || { warn "Server 头为 '${SERVER_HDR}'"; FAILED=1; }

echo "  · 隐藏路径"
for p in /admin /dealer /manage /panel /backend; do
    code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:3000${p}")
    [[ "${code}" == "404" ]] || { warn "${p} 返回 ${code}（应为 404）"; FAILED=1; }
done
ok "隐藏路径均为 404"

echo "  · 数据库完整性"
if php bin/check-database.php >/tmp/dbcheck.log 2>&1; then
    ok "$(tail -1 /tmp/dbcheck.log)"
else
    warn "有失败项，详见 /tmp/dbcheck.log"
    tail -5 /tmp/dbcheck.log
    FAILED=1
fi

echo "  · 登录链路"
if php bin/verify-login.php >/tmp/logincheck.log 2>&1; then
    ok "$(tail -1 /tmp/logincheck.log)"
else
    warn "有失败项，详见 /tmp/logincheck.log"
    tail -5 /tmp/logincheck.log
    FAILED=1
fi

echo "  · 回归测试"
if php bin/regression.php >/tmp/reg.log 2>&1; then
    ok "$(tail -1 /tmp/reg.log)"
else
    warn "有失败项，详见 /tmp/reg.log"
    tail -5 /tmp/reg.log
    FAILED=1
fi

echo
if [[ ${FAILED} -eq 0 ]]; then
    printf '\033[1;32m═══ 部署完成，全部自检通过 ═══\033[0m\n'
    echo
    echo "  服务：systemctl status ltzy-api"
    echo "  日志：tail -f ${APP_DIR}/var/server.out.log"
    echo "  访问：http://127.0.0.1:3000（外部经 nginx）"
else
    printf '\033[1;33m═══ 已启动，但有自检项未通过，请看上面的 warn ═══\033[0m\n'
fi
