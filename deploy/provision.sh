#!/usr/bin/env bash
#
# 全新系统环境装配（Debian/Ubuntu）
#
# 用法：
#   bash deploy/provision.sh
#   PHP_VER=8.5 PG_PKG=17 bash deploy/provision.sh
#
# 做四件事：系统基线 → PostgreSQL → PHP + OpenSwoole → nginx 反代配置。
# 不做代码部署、不建库导数据 —— 那些在 release.sh 里，因为要配合上传时机。
#
# 每一步都是幂等的：重复跑不会装两次、不会覆盖已有配置（有则跳过并提示）。

set -euo pipefail

PHP_VER="${PHP_VER:-8.5}"
DOMAIN="${DOMAIN:-_}"
APP_DIR="${APP_DIR:-/opt/llm-gateway}"
DB_USER="${DB_USER:-llm_gateway}"
DB_NAME="${DB_NAME:-llm_gateway}"

log()  { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()   { printf '  \033[1;32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[1;33m!\033[0m %s\n' "$*"; }
die()  { printf '\n\033[1;31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "需要 root 运行：sudo bash $0"
command -v apt-get >/dev/null || die "本脚本只支持 apt 系发行版"

# ── 1. 系统基线 ────────────────────────────────────────────
log "1/5 系统基线"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get -y -qq upgrade
apt-get -y -qq install \
    curl gnupg ca-certificates lsb-release apt-transport-https \
    git unzip htop vim ufw fail2ban rsync jq
ok "基础包已安装"

timedatectl set-timezone Asia/Shanghai 2>/dev/null || warn "时区设置失败（容器环境可忽略）"
ok "时区 Asia/Shanghai"

# 防火墙：只开 SSH/HTTP/HTTPS。
# 应用端口 3000 必须只监听本机，否则 Swoole 的 Server 头会直连泄露。
if command -v ufw >/dev/null; then
    ufw --force default deny incoming >/dev/null
    ufw --force default allow outgoing >/dev/null
    ufw allow 22/tcp  >/dev/null
    ufw allow 80/tcp  >/dev/null
    ufw allow 443/tcp >/dev/null
    ufw --force enable >/dev/null
    ok "ufw 已启用（22/80/443）"
fi

# 小机器（2G 内存）没有 swap 会在装大包时 OOM
if ! swapon --show | grep -q .; then
    fallocate -l 1G /swapfile 2>/dev/null && chmod 600 /swapfile \
      && mkswap /swapfile >/dev/null && swapon /swapfile \
      && echo '/swapfile none swap sw 0 0' >> /etc/fstab \
      && ok "已加 1G swap" || warn "swap 创建失败，继续"
fi

# ── 2. PostgreSQL ──────────────────────────────────────────
log "2/5 PostgreSQL（官方源，最新稳定）"
if ! dpkg -l | grep -q '^ii  postgresql '; then
    install -d /usr/share/postgresql-common/pgdg
    curl -fsSL -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
        https://www.postgresql.org/media/keys/ACCC4CF8.asc
    echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] \
https://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" \
        > /etc/apt/sources.list.d/pgdg.list
    apt-get update -qq
    apt-get -y -qq install postgresql postgresql-contrib
    ok "已安装 $(psql --version | awk '{print $3}')"
else
    ok "已存在，跳过：$(psql --version 2>/dev/null | awk '{print $3}' || echo unknown)"
fi

systemctl enable --now postgresql

# ── 按 2C2G 小机器调参 ──
# 默认配置是给 4G+ 机器用的，2G 内存照搬会 OOM。
# 这里按「PG 占 40%、PHP/Swoole 占 40%、系统留 20%」分配。
PG_CONF=$(ls -1 /etc/postgresql/*/main/postgresql.conf 2>/dev/null | head -1)
if [[ -n "${PG_CONF}" ]]; then
    # 先删掉可能存在的旧配置块，避免重复追加
    sed -i '/^# === APP TUNING ===/,/^# === APP TUNING END ===/d' "${PG_CONF}"
    cat >> "${PG_CONF}" <<'PGTUNE'

# === APP TUNING ===
# 2C2G 小机器：PG 给 256MB 共享缓冲，够小规模部署使用
max_connections = 100
shared_buffers = 256MB
effective_cache_size = 512MB
work_mem = 8MB
maintenance_work_mem = 32MB
random_page_cost = 1.1
effective_io_concurrency = 200
wal_buffers = 16MB
min_wal_size = 80MB
max_wal_size = 512MB
checkpoint_completion_target = 0.9
default_statistics_target = 100
# 关掉不需要的日志，省 IO
log_min_duration_statement = 1000
# === APP TUNING END ===
PGTUNE
    ok "PostgreSQL 已按 2C2G 调参：$(basename $(dirname $(dirname "${PG_CONF}")))"
fi

systemctl restart postgresql

# 建用户与库（已存在则跳过，不覆盖密码）
if ! su - postgres -c "psql -tAc \"SELECT 1 FROM pg_roles WHERE rolname='${DB_USER}'\"" | grep -q 1; then
    DB_PASS="$(openssl rand -base64 24 | tr -d '/+=' | head -c 28)"
    su - postgres -c "psql -c \"CREATE USER ${DB_USER} WITH PASSWORD '${DB_PASS}';\""
    ok "已建库用户 ${DB_USER}"
    warn "数据库密码（只显示这一次，立刻记到 config/database.php）："
    printf '\n    \033[1;33m%s\033[0m\n\n' "${DB_PASS}"
else
    ok "库用户 ${DB_USER} 已存在，密码未改动"
fi

if ! su - postgres -c "psql -tAc \"SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'\"" | grep -q 1; then
    su - postgres -c "psql -c \"CREATE DATABASE ${DB_NAME} OWNER ${DB_USER} ENCODING 'UTF8';\""
    ok "已建库 ${DB_NAME}"
else
    ok "库 ${DB_NAME} 已存在"
fi

# ── 3. PHP + 扩展 ─────────────────────────────────────────
log "3/5 PHP ${PHP_VER} + 扩展"
if ! ls /etc/apt/sources.list.d/ | grep -q sury; then
    curl -fsSLo /usr/share/keyrings/deb.sury.org-php.gpg https://packages.sury.org/php/apt.gpg
    echo "deb [signed-by=/usr/share/keyrings/deb.sury.org-php.gpg] \
https://packages.sury.org/php/ $(lsb_release -sc) main" \
        > /etc/apt/sources.list.d/sury-php.list
    apt-get update -qq
fi

# ⚠️ 不要装 php8.5-opcache：sury 里这个包只到 8.4。
#    PHP 8.5 起 OPcache 直接编进 php8.5-common/cli，不再是独立包，
#    apt 会报 "Unable to locate package php8.5-opcache" 并让整条命令失败。
apt-get -y -qq install \
    "php${PHP_VER}-cli" "php${PHP_VER}-pgsql" "php${PHP_VER}-curl" \
    "php${PHP_VER}-mbstring" "php${PHP_VER}-xml" "php${PHP_VER}-intl" \
    "php${PHP_VER}-dev" "php${PHP_VER}-zip" \
    php-pear build-essential autoconf pkg-config libssl-dev
ok "PHP 已安装：$(php -r 'echo PHP_VERSION;')"

# OPcache 确认（8.5 内置，这里只验证它在不在）
if php -m | grep -qix 'Zend OPcache'; then
    ok "OPcache 已随 PHP 内置启用"
else
    warn "OPcache 未启用（8.5 应内置，可能需要 phpenmod opcache）"
    phpenmod -v "${PHP_VER}" opcache 2>/dev/null || true
fi

# ── 4. OpenSwoole ─────────────────────────────────────────
log "4/5 OpenSwoole"
if php -m | grep -qi openswoole; then
    ok "已安装：$(php -r 'echo SWOOLE_VERSION;' 2>/dev/null || echo '')"
else
    # ⚠️ 不能用 `pecl install`：它会交互式追问 openssl/http2/sockets 三四个选项，
    #    在无人值守的脚本里会直接卡死到超时。改成 `pecl download` 拿到源码包，
    #    再用 phpize 手动编译，参数在命令行一次给全。
    #
    # 必须开的三个：openssl（上游全是 HTTPS，不开连不通）、
    #              http2、sockets。
    cd /tmp
    rm -rf openswoole-* ext-openswoole-*
    pecl download openswoole >/dev/null

    SRC=$(ls -1 openswoole-*.tgz | head -1)
    [[ -n "${SRC}" ]] || die "pecl download openswoole 失败"
    tar -xzf "${SRC}"
    cd openswoole-*/

    # ⚠️ 不能加 --enable-sockets。
    #    PHP 8.5 把 sockets 编成了**内置**扩展（php8.5-common 直接 Provides 它），
    #    符号不导出给外部 .so。OpenSwoole 加了 --enable-sockets 会引用 socket_ce，
    #    编译能过，但加载时报 "undefined symbol: socket_ce"，整个扩展废掉。
    #
    #    不需要它：我们的协程 hook 走 SWOOLE_HOOK_ALL，用的是 stream/coroutine 那套，
    #    不依赖 sockets 扩展的符号。
    #
    # 必须开的是 openssl —— 上游全是 HTTPS，不开连不通。
    phpize >/dev/null
    ./configure --enable-openssl --enable-http2 >/dev/null
    make -j"$(nproc)" >/dev/null
    make install >/dev/null

    echo "extension=openswoole.so" > "/etc/php/${PHP_VER}/mods-available/openswoole.ini"
    phpenmod -v "${PHP_VER}" openswoole
    cd /tmp && rm -rf openswoole-* ext-openswoole-*

    ok "已安装：$(php -r 'echo SWOOLE_VERSION;')"
fi

# 缺扩展就别往下走了，晚发现代价更大
log "扩展自检"
MISSING=()
for ext in pdo pdo_pgsql pgsql curl json mbstring openssl sockets zlib intl swoole; do
    php -m | grep -qix "${ext}" || MISSING+=("${ext}")
done
if [[ ${#MISSING[@]} -gt 0 ]]; then
    die "缺扩展：${MISSING[*]}"
fi
ok "pdo/pdo_pgsql/pgsql/curl/json/mbstring/openssl/sockets/zlib/intl/swoole 全部就绪"

# ── 5. nginx ──────────────────────────────────────────────
log "5/5 nginx 反代"
apt-get -y -qq install nginx
cat > /etc/nginx/sites-available/llm-gateway <<NGINX
server {
    listen 80;
    server_name ${DOMAIN};

    client_max_body_size 100m;          # 与 package_max_length 对齐

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;

        # ⚠️ 流式（SSE）必须关缓冲：开了会导致前端等整段响应结束才收到内容
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header X-Accel-Buffering no;

        # 上游超时 300s，nginx 必须比它更宽容
        proxy_read_timeout 360s;
        proxy_send_timeout 360s;
        proxy_connect_timeout 10s;

        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        # 不暴露后端技术栈
        proxy_hide_header Server;
        add_header Server nginx;
    }
}
NGINX

ln -sf /etc/nginx/sites-available/llm-gateway /etc/nginx/sites-enabled/llm-gateway
rm -f /etc/nginx/sites-enabled/default
nginx -t && systemctl reload nginx
ok "nginx 配置已就位（server_name=${DOMAIN}）"

# ── 收尾 ──────────────────────────────────────────────────
log "环境装配完成"
cat <<'SUMMARY'

下一步（在本地做完再上传）：

  1. 把上面打印的数据库密码写进 config/database.php
  2. rsync 上传代码到 /opt/llm-gateway
  3. bash deploy/release.sh        # 建目录、装 systemd、跑迁移、自检
  4. 配 HTTPS：certbot --nginx -d 你的域名

别忘了：应用只监听 127.0.0.1:3000，外部走 nginx。
SUMMARY
