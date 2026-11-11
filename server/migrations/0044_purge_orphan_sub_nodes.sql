-- 清理订阅侧悬挂数据：节点绑定的服务器已被删除
--
-- 背景：serverDelete 之前只删 node_configs / metrics / push_logs / servers，
-- 没有联动 singbox_nodes / mihomo_nodes / mtproto_nodes，于是「代理授权」
-- 里会残留一行没有名字、却仍显示「N 个服务」的授权，且配置还指向这些节点。
--
-- 0043 之后 serverDelete 已在删 servers 之前清理这三张表与 configs；
-- 这条迁移负责清理存量。

-- 1) 用户订阅里指向「节点已不存在」的配置
DELETE c FROM configs c
  LEFT JOIN singbox_nodes n ON n.id = c.singbox_node_id
 WHERE c.singbox_node_id > 0 AND n.id IS NULL;

-- 2) 三种节点绑定：server_id 指向已删除的服务器（server_id=0 的手工节点不受影响）
DELETE FROM singbox_nodes
 WHERE server_id > 0
   AND server_id NOT IN (SELECT id FROM servers);

DELETE FROM mihomo_nodes
 WHERE server_id > 0
   AND server_id NOT IN (SELECT id FROM servers);

DELETE FROM mtproto_nodes
 WHERE server_id > 0
   AND server_id NOT IN (SELECT id FROM servers);
