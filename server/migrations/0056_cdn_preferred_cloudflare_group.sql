-- CDN 优选默认分组改名：default → cloudflare（task-18）
--
-- 背景：
--   面板此前只有默认分组，group_name 写死为 'default'；现在需要为其他类型 CDN
--   （aliyun / tencent / overseas 等）预留多分组能力，默认分组改名为 cloudflare，
--   表示「Cloudflare 优选地址」这一组。
--
-- 兼容：
--   * 代码层 cdnPreferredForGroup 对空串 / 旧 'default' 都会映射到 cloudflare，
--     所以模板里显式配置 group=default 的旧配置依旧生效；
--   * 本迁移只改库里的存量行（线上现有 14 条 default），可重复执行（幂等）：
--     第二次执行时 WHERE 已无 default 行，影响 0 行。
UPDATE `cdn_preferred_addresses`
   SET `group_name` = 'cloudflare',
       `updated_at` = UNIX_TIMESTAMP(NOW(3)) * 1000
 WHERE LOWER(TRIM(`group_name`)) = 'default';