-- 清理 VPN 节点相关的悬挂数据
--
-- 背景：
--   1) DeleteNode 之前只删 vpn_nodes，授权（vpn_node_grants）与会话
--      （vpn_sessions）留在表里，管理端「授权」列表仍然显示这台已删除的节点。
--   2) 删除底层服务器（servers 表）时完全没有联动 VPN 节点，于是
--      vpn_nodes 里留下 server_id 指向已消失服务器的行，连带它的授权一起悬挂。
--
-- 0042 之后：
--   * vpn.Store.DeleteNode 改为事务级联删除
--   * serverDelete 会先清掉关联的 VPN 节点与授权
-- 这条迁移负责清理存量脏数据。

-- 1) 授权/会话指向的节点已经不存在
DELETE g FROM vpn_node_grants g
  LEFT JOIN vpn_nodes n ON n.id = g.node_id
 WHERE n.id IS NULL;

DELETE s FROM vpn_sessions s
  LEFT JOIN vpn_nodes n ON n.id = s.node_id
 WHERE n.id IS NULL;

-- 2) VPN 节点绑定的服务器已经不存在（server_id > 0 才判断，
--    手工创建的节点 server_id = 0 不受影响）
DELETE g FROM vpn_node_grants g
  JOIN vpn_nodes n ON n.id = g.node_id
  LEFT JOIN servers s ON s.id = n.server_id
 WHERE n.server_id > 0 AND s.id IS NULL;

DELETE ss FROM vpn_sessions ss
  JOIN vpn_nodes n ON n.id = ss.node_id
  LEFT JOIN servers s ON s.id = n.server_id
 WHERE n.server_id > 0 AND s.id IS NULL;

DELETE n FROM vpn_nodes n
  LEFT JOIN servers s ON s.id = n.server_id
 WHERE n.server_id > 0 AND s.id IS NULL;
