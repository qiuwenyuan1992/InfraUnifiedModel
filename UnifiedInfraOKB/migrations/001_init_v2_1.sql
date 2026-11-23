-- OKB v2.1 初始化迁移（MySQL 8.0+）
-- 依据 storage-model.md v2.1：不可变层 + 图谱层（edge_kind 三分/severity/node_kind/结构化 signature）+ 证据与修订层 + 场景层。
-- 表名与 GORM 默认 NamingStrategy 一致（复数）；既有 v2 库请改用 002_alter_v2_1.sql 增量升级，勿重跑本文件。
-- 执行方式：mysql -u <user> -p <db> < migrations/001_init_v2_1.sql
-- 幂等性：CREATE TABLE IF NOT EXISTS；不覆盖既有数据。

SET NAMES utf8mb4;

-- ---------- 不可变层 ----------

CREATE TABLE IF NOT EXISTS `sources` (
  `id`                 VARCHAR(36)  NOT NULL PRIMARY KEY,
  `kind`              VARCHAR(20)  NOT NULL COMMENT 'internal_case/vendor_doc/expert_input/alert_strategy/alert_event/system_code/industry_report',
  `title`             VARCHAR(256) NOT NULL,
  `sensitivity_cleared` TINYINT(1)  NOT NULL DEFAULT 0 COMMENT '脱敏校验通过标记；未通过不得入图',
  `permission_level`  INT          NOT NULL DEFAULT 0,
  `storage_uri`       VARCHAR(512) DEFAULT NULL,
  `created_at`        DATETIME     NOT NULL,
  KEY `idx_source_kind` (`kind`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='数据源登记';

CREATE TABLE IF NOT EXISTS `source_versions` (
  `id`           VARCHAR(36)  NOT NULL PRIMARY KEY,
  `source_id`    VARCHAR(36)  NOT NULL,
  `content_hash` VARCHAR(64)  NOT NULL COMMENT 'sha256 内容寻址',
  `storage_uri`  VARCHAR(512) DEFAULT NULL,
  `created_at`   DATETIME     NOT NULL,
  UNIQUE KEY `uk_sv_hash` (`content_hash`),
  KEY `idx_sv_source` (`source_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='原件版本（不可变）';

CREATE TABLE IF NOT EXISTS `parsed_versions` (
  `id`               VARCHAR(36) NOT NULL PRIMARY KEY,
  `source_version_id` VARCHAR(36) NOT NULL,
  `parser_version`   VARCHAR(32) NOT NULL,
  `content_hash`     VARCHAR(64) DEFAULT NULL,
  `created_at`       DATETIME    NOT NULL,
  KEY `idx_pv_sv` (`source_version_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='解析版本（不可变）';

CREATE TABLE IF NOT EXISTS `fragments` (
  `id`                VARCHAR(36)  NOT NULL PRIMARY KEY,
  `parsed_version_id` VARCHAR(36)  NOT NULL,
  `char_start`       INT          NOT NULL DEFAULT 0,
  `char_end`          INT          NOT NULL DEFAULT 0,
  `page`             INT          NOT NULL DEFAULT 0,
  `paragraph_path`   VARCHAR(256) DEFAULT NULL,
  `summary`          VARCHAR(512) DEFAULT NULL COMMENT '摘要：未授权调用方唯一可见内容',
  `content`          MEDIUMTEXT   COMMENT '原文内容（脱敏后）',
  `permission_level` INT          NOT NULL DEFAULT 0,
  `created_at`       DATETIME     NOT NULL,
  KEY `idx_frag_pv` (`parsed_version_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='文本片段（出处回链）';

-- ---------- 图谱层 ----------

CREATE TABLE IF NOT EXISTS `anomaly_types` (
  `id`                 VARCHAR(36)  NOT NULL PRIMARY KEY,
  `stable_code`        VARCHAR(128) NOT NULL,
  `name`               VARCHAR(256) NOT NULL,
  `category`           VARCHAR(64)  DEFAULT NULL,
  `domain`             VARCHAR(32)  DEFAULT NULL COMMENT 'v2.1 三域：server/network/dci',
  `severity_online`    VARCHAR(8)   DEFAULT NULL COMMENT 'v2.1 线上等级 P0-P4（样本优先仲裁）',
  `severity_dict`      VARCHAR(8)   DEFAULT NULL COMMENT 'v2.1 字典等级 P1-P3',
  `node_kind`          VARCHAR(16)   NOT NULL DEFAULT 'atomic' COMMENT 'v2.1 atomic/aggregate/placeholder/event',
  `clock_hint`         VARCHAR(16)  DEFAULT NULL COMMENT 'v2.1 处置时钟：seconds/minutes/hours',
  `current_revision_id` VARCHAR(36) DEFAULT NULL,
  `status`             VARCHAR(16)  NOT NULL DEFAULT 'draft' COMMENT 'draft/curated 两态',
  `created_at`         DATETIME     NOT NULL,
  `updated_at`         DATETIME     NOT NULL,
  UNIQUE KEY `uk_anomaly_code` (`stable_code`),
  KEY `idx_anomaly_domain` (`domain`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='异常节点';

CREATE TABLE IF NOT EXISTS `anomaly_aliases` (
  `id`         VARCHAR(36)  NOT NULL PRIMARY KEY,
  `anomaly_id` VARCHAR(36)  NOT NULL,
  `alias`      VARCHAR(256) NOT NULL,
  `vendor`     VARCHAR(64)  DEFAULT NULL,
  `confirmed`  TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '人工确认后才参与对齐',
  `created_at` DATETIME     NOT NULL,
  KEY `idx_alias_anomaly` (`anomaly_id`),
  KEY `idx_alias_text` (`alias`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='厂商别名对齐表';

CREATE TABLE IF NOT EXISTS `causal_rules` (
  `id`                     VARCHAR(36)  NOT NULL PRIMARY KEY,
  `source_anomaly_id`      VARCHAR(36)  NOT NULL,
  `target_anomaly_id`      VARCHAR(36)  NOT NULL,
  `edge_kind`              VARCHAR(16)  NOT NULL DEFAULT 'causal' COMMENT 'v2.1 三分：causal(可遍历)/same_event/same_family(仅标注返回)',
  `edge_type`              VARCHAR(16)  NOT NULL DEFAULT 'trigger' COMMENT '仅 causal 边：trigger/indicate/mitigate/prevent',
  `mitigated_via_root_cause` VARCHAR(36) DEFAULT NULL COMMENT 'mitigate 边根因挂靠',
  `conditions`             TEXT         COMMENT 'JSON []ConditionClause 受控条件',
  `time_constraints`       TEXT         COMMENT 'JSON 时间约束（含传播延迟档 seconds/minutes/hours）',
  `evidence_level`         VARCHAR(20)  NOT NULL COMMENT 'case_verified/runtime_verified/doc_explicit/inferred/expert_only',
  `confidence`             DOUBLE       NOT NULL DEFAULT 0,
  `case_support_count`     INT          NOT NULL DEFAULT 0 COMMENT '独立来源印证计数（≥2 升 curated）',
  `current_revision_id`    VARCHAR(36)  DEFAULT NULL,
  `status`                 VARCHAR(16)  NOT NULL DEFAULT 'draft',
  `review_flagged`         TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '否决降级后的复核标记',
  `created_at`             DATETIME     NOT NULL,
  `updated_at`             DATETIME     NOT NULL,
  KEY `idx_edge_source` (`source_anomaly_id`, `edge_kind`, `status`),
  KEY `idx_edge_target` (`target_anomaly_id`, `edge_kind`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='因果边与非因果关联（v2.1 边语义三分）';

CREATE TABLE IF NOT EXISTS `alert_mappings` (
  `id`                 VARCHAR(36)  NOT NULL PRIMARY KEY,
  `alert_signature`    VARCHAR(256) NOT NULL COMMENT '拼接键 domain|channel|sig_key 或旧式单串',
  `domain`             VARCHAR(32)  DEFAULT NULL COMMENT 'v2.1 server/network/dci',
  `channel`            VARCHAR(32)  DEFAULT NULL COMMENT 'v2.1 agent_syslog/bmc_syslog/snmptrap/metric/dci_point/ufm/enms',
  `sig_key`            VARCHAR(512) DEFAULT NULL COMMENT 'v2.1 通道定形键值',
  `upgrade_ref`        VARCHAR(64)  DEFAULT NULL COMMENT 'v2.1 升级链关联策略 id',
  `anomaly_id`         VARCHAR(36)  NOT NULL,
  `confidence`         DOUBLE       NOT NULL DEFAULT 0,
  `evidence_level`     VARCHAR(20)  NOT NULL,
  `current_revision_id` VARCHAR(36)  DEFAULT NULL,
  `status`             VARCHAR(16)  NOT NULL DEFAULT 'draft',
  `review_flagged`     TINYINT(1)   NOT NULL DEFAULT 0,
  `created_at`         DATETIME     NOT NULL,
  `updated_at`         DATETIME     NOT NULL,
  KEY `idx_map_sig` (`alert_signature`),
  KEY `idx_map_structured` (`domain`, `channel`, `sig_key`(191)),
  KEY `idx_map_anomaly` (`anomaly_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='告警入口（v2.1 结构化 signature）';

-- ---------- 证据与修订层 ----------

CREATE TABLE IF NOT EXISTS `evidences` (
  `id`           VARCHAR(36) NOT NULL PRIMARY KEY,
  `kind`         VARCHAR(20) NOT NULL COMMENT 'case/runtime/doc/inferred/expert',
  `entity_type`  VARCHAR(20) NOT NULL COMMENT 'anomaly_type/causal_rule/alert_mapping',
  `entity_id`    VARCHAR(36) NOT NULL,
  `fragment_id`  VARCHAR(36) DEFAULT NULL COMMENT '出处回链',
  `source_id`    VARCHAR(36) NOT NULL COMMENT '独立性按此去重计数',
  `created_by`   VARCHAR(10) NOT NULL COMMENT 'model/expert',
  `note`         VARCHAR(512) DEFAULT NULL,
  `created_at`   DATETIME    NOT NULL,
  KEY `idx_ev_entity` (`entity_type`, `entity_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='统一证据记录';

CREATE TABLE IF NOT EXISTS `revisions` (
  `id`               VARCHAR(36) NOT NULL PRIMARY KEY,
  `entity_type`      VARCHAR(20) NOT NULL,
  `entity_id`        VARCHAR(36) NOT NULL,
  `content_snapshot` MEDIUMTEXT  NOT NULL COMMENT '变更时点 JSON 快照（仲裁原值留痕于此）',
  `prev_revision_id` VARCHAR(36) DEFAULT NULL,
  `changed_by`       VARCHAR(128) NOT NULL,
  `reason`           VARCHAR(512) DEFAULT NULL,
  `created_at`       DATETIME    NOT NULL,
  KEY `idx_rev_entity` (`entity_type`, `entity_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='追加式修订历史（不覆盖）';

CREATE TABLE IF NOT EXISTS `feedbacks` (
  `id`            VARCHAR(36)  NOT NULL PRIMARY KEY,
  `diagnosis_ref` VARCHAR(128) NOT NULL,
  `edge_id`       VARCHAR(36)  NOT NULL,
  `verdict`       VARCHAR(12)  NOT NULL COMMENT 'confirmed/rejected',
  `operator_id`   VARCHAR(128) NOT NULL,
  `notes`         VARCHAR(512) DEFAULT NULL,
  `created_at`    DATETIME     NOT NULL,
  UNIQUE KEY `uk_feedback` (`diagnosis_ref`, `edge_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='运行时反馈（幂等）';

-- ---------- 场景层 ----------

CREATE TABLE IF NOT EXISTS `scenarios` (
  `id`          VARCHAR(36)  NOT NULL PRIMARY KEY,
  `code`        VARCHAR(128) NOT NULL,
  `name`        VARCHAR(128) NOT NULL,
  `domain`      VARCHAR(32)  DEFAULT NULL,
  `description` VARCHAR(512) DEFAULT NULL,
  `created_at`  DATETIME     NOT NULL,
  `updated_at`  DATETIME     NOT NULL,
  UNIQUE KEY `uk_scenario_code` (`code`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='场景（组织维度）';

CREATE TABLE IF NOT EXISTS `scenario_nodes` (
  `id`         VARCHAR(36) NOT NULL PRIMARY KEY,
  `scenario_id` VARCHAR(36) NOT NULL,
  `anomaly_id` VARCHAR(36) NOT NULL,
  `created_at` DATETIME    NOT NULL,
  UNIQUE KEY `uk_scenario_node` (`scenario_id`, `anomaly_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='场景成员指派';
