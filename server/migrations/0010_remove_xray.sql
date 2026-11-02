-- 0010：Xray 下线（V15.6）
--
-- 背景：vless-xhttp 是唯一由 Xray 承载的协议，随 Xray 一并下线；
--       sing-box 的 vless-reality（13705）已覆盖同类场景。
-- 说明：xray_path / service_xray / enable_xray / xhttp_path / enable_xhttp
--       这些列保留（MySQL 删列不可逆，回滚需要），只是代码不再读写。
-- 本迁移只做数据面清理；节点上残留的 xray 服务与 /etc/sing-box/xray.json
-- 不会自动删除（仅停用不再下发），需要时用 WebSSH 手工清理。

-- 1) 模板：关掉 xray 引擎与版本，warp 引擎只留 sing-box
UPDATE `singbox_templates`
   SET `enable_xray` = 0,
       `xray_version` = '',
       `warp_engines` = 'singbox'
 WHERE IFNULL(`enable_xray`, 0) <> 0
    OR IFNULL(`xray_version`, '') <> ''
    OR IFNULL(`warp_engines`, '') <> 'singbox';

-- 2) 模板参数里的 xhttp 段（JSON 非法时跳过，不阻断迁移）
UPDATE `singbox_templates`
   SET `protocol_options` = JSON_REMOVE(`protocol_options`, '$.xhttp')
 WHERE JSON_VALID(`protocol_options`)
   AND JSON_CONTAINS_PATH(`protocol_options`, 'one', '$.xhttp');

-- 3) 已下发的 xray.json 配置：删掉记录，Agent 不再收到该任务
DELETE FROM `node_configs`
 WHERE `engine` = 'xray'
    OR `target_path` LIKE '%xray.json';

-- 4) 订阅里由节点管理的 xhttp 链接一并清掉（外部订阅源导入的配置不动）
DELETE FROM `configs`
 WHERE `singbox_node_id` > 0
   AND `config_data` LIKE '%type=xhttp%';

-- 5) 清空节点指纹，强制下一次 reconcile 按新协议集合重新生成链接
UPDATE `singbox_nodes`
   SET `last_link_hash` = '',
       `last_render_hash` = ''
 WHERE IFNULL(`last_link_hash`, '') <> ''
    OR IFNULL(`last_render_hash`, '') <> ''
