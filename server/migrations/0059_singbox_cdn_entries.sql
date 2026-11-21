-- 0059: sing-box 节点多 CDN 条目（V1.5.0）
--
-- 背景：
--   原先一个节点只支持一个 cdn_domain；现在一个节点最多 8 个 CDN，每个 CDN
--   有自定义名称（CF/ESA/EO）、主 CDN 标记和可选的优选分组。
--
-- 存储：
--   * 新列 cdn_entries LONGTEXT，存 JSON 对象数组：
--     [{"name":"CF","domain":"cf.example.com","primary":true,"prefer_group":"cloudflare"}]
--   * 保留旧列 cdn_domain（=主 CDN 域名），供旧前端/旧订阅逻辑兼容。
--
-- 回填：
--   旧数据非空 cdn_domain 转成单元素对象数组（name=""; primary=true; prefer_group=""）。
--   UPDATE 带 cdn_entries 为空的条件，幂等可安全重复执行（第二次影响 0 行）。
--   ALTER 由迁移器按版本只执行一次（schema_migrations 记录）。

ALTER TABLE `singbox_nodes`
  ADD COLUMN `cdn_entries` LONGTEXT NULL COMMENT '多 CDN 条目 JSON 数组（name/domain/primary/prefer_group）';

UPDATE `singbox_nodes`
   SET `cdn_entries` = JSON_ARRAY(JSON_OBJECT(
         'name', '',
         'domain', LOWER(TRIM(`cdn_domain`)),
         'primary', TRUE,
         'prefer_group', ''
       ))
 WHERE `cdn_domain` IS NOT NULL
   AND TRIM(`cdn_domain`) <> ''
   AND (`cdn_entries` IS NULL OR TRIM(`cdn_entries`) IN ('', '[]', 'null'));