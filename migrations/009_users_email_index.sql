-- 009_users_email_index.sql
--
-- 为什么需要这个索引：
--   导入的 261 个 special 账号，username 很多是旧系统编号，用户实际记的是邮箱。
--   UserRepository::findByUsername() 发现入参含 @ 时会回退到 WHERE email = ?，
--   而 users.email 上原本没有任何索引 —— 这个分支就是一次全表扫描。
--   现在 269 行扫描无所谓，但登录是热路径，且表会随导入继续变大。
--
-- 为什么用部分索引而不是普通索引：
--   全表 269 行里只有 261 行 email 非空，剩下的是空串占位。
--   WHERE email <> '' 把空串行排除在索引外，索引更小；
--   PG 对 email = 'x@y.z' 这种等值查询能自动加上 <> '' 的谓词推断，
--   所以部分索引对「按邮箱查人」完全有效。
--
-- 注意：本文件不要写 BEGIN/COMMIT，migrate.php 已经在事务里包了一层。

CREATE INDEX IF NOT EXISTS idx_users_email
    ON users (email)
    WHERE email <> '';
