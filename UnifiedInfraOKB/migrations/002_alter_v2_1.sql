-- OKB v2 → v2.1 增量迁移（MySQL 8.0+）
-- 适用：已按 v2 schema 建库的既有环境（表为 GORM 复数命名）。
-- 全新库直接用 001_init_v2_1.sql，勿执行本文件。
-- 注意：MySQL 的 ADD COLUMN 无 IF NOT EXISTS，本脚本一次性执行；重复执行会报列已存在。
-- 执行方式：mysql -u <user> -p <db> < migrations/002_alter_v2_1.sql

-- 图节点：v2.1 等级/类型/时钟/域
ALTER TABLE `anomaly_types`
  ADD COLUMN `domain` VARCHAR(32) DEFAULT NULL COMMENT 'v2.1 三域：server/network/dci' AFTER `category`,
  ADD COLUMN `severity_online` VARCHAR(8) DEFAULT NULL COMMENT 'v2.1 线上等级 P0-P4（样本优先仲裁）' AFTER `domain`,
  ADD COLUMN `severity_dict` VARCHAR(8) DEFAULT NULL COMMENT 'v2.1 字典等级 P1-P3' AFTER `severity_online`,
  ADD COLUMN `node_kind` VARCHAR(16) NOT NULL DEFAULT 'atomic' COMMENT 'v2.1 atomic/aggregate/placeholder/event' AFTER `severity_dict`,
  ADD COLUMN `clock_hint` VARCHAR(16) DEFAULT NULL COMMENT 'v2.1 处置时钟：seconds/minutes/hours' AFTER `node_kind`,
  ADD INDEX `idx_anomaly_types_domain` (`domain`);

-- 图边：v2.1 边语义三分。旧数据（trigger/indicate/mitigate/prevent）自动归为 causal。
ALTER TABLE `causal_rules`
  ADD COLUMN `edge_kind` VARCHAR(16) NOT NULL DEFAULT 'causal' COMMENT 'v2.1 三分：causal(可遍历)/same_event/same_family(仅标注返回)' AFTER `target_anomaly_id`,
  ADD INDEX `idx_causal_rules_kind` (`edge_kind`);

-- 告警入口：v2.1 结构化 signature（域+采集通道+通道定形键值）
ALTER TABLE `alert_mappings`
  ADD COLUMN `domain` VARCHAR(32) DEFAULT NULL COMMENT 'v2.1 server/network/dci' AFTER `alert_signature`,
  ADD COLUMN `channel` VARCHAR(32) DEFAULT NULL COMMENT 'v2.1 agent_syslog/bmc_syslog/snmptrap/metric/dci_point/ufm/enms' AFTER `domain`,
  ADD COLUMN `sig_key` VARCHAR(512) DEFAULT NULL COMMENT 'v2.1 通道定形键值' AFTER `channel`,
  ADD COLUMN `upgrade_ref` VARCHAR(64) DEFAULT NULL COMMENT 'v2.1 升级链关联策略 id' AFTER `sig_key`,
  ADD INDEX `idx_alert_mappings_structured` (`domain`, `channel`, `sig_key`(191));

-- 说明：sources.kind 为 VARCHAR(20)，新枚举值（alert_strategy/alert_event/system_code/industry_report）
-- 与 evidences.kind（runtime）无需 Schema 变更，直接写入即可。
