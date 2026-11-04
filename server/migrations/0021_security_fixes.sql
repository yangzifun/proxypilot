-- 0021: 安全加固（并发去重）
ALTER TABLE `batch_jobs`
  ADD COLUMN `dedup_key` VARCHAR(191) NULL,
  ADD UNIQUE KEY `uk_batch_jobs_dedup` (`dedup_key`);

ALTER TABLE `admin_email_codes`
  ADD KEY `idx_admin_email_codes_rate` (`email`, `purpose`, `created_at`);
