-- 0036: 同一节点/同一用户只保留一条授权；去重并加唯一键
DELETE g1 FROM vpn_node_grants g1
  JOIN vpn_node_grants g2
    ON g1.node_id = g2.node_id
   AND g1.subject_type = g2.subject_type
   AND g1.subject_id = g2.subject_id
   AND g1.id < g2.id;

ALTER TABLE vpn_node_grants
  ADD UNIQUE KEY uniq_vpn_node_subject (node_id, subject_type, subject_id);
