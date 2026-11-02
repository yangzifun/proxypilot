-- 0009: 对齐 masque_warp_profiles.uuid 与 uuid_info.uuid 的排序规则
ALTER TABLE `masque_warp_profiles`
  MODIFY `uuid` VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_cs NOT NULL;
