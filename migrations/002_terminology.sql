-- 跨机构术语与用途协议：中心只保存不可逆摘要与协议元数据，不接收原始病历。

-- 机构提交的本地字段定义摘要。definition_digest 为 SHA-256 十六进制，
-- 中心无法也不应还原原始定义文本。
CREATE TABLE IF NOT EXISTS term_submissions (
    term_id TEXT PRIMARY KEY,
    institution_ref TEXT NOT NULL,
    local_code TEXT NOT NULL,
    definition_digest TEXT NOT NULL,
    language TEXT NOT NULL,
    unit TEXT NOT NULL DEFAULT '',
    authorized_purposes TEXT NOT NULL,          -- JSON 数组，授权用途
    submitted_by TEXT NOT NULL,
    submitted_at TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',      -- active | withdrawn
    withdrawn_by TEXT,
    withdrawn_at TEXT,
    withdrawal_scope TEXT                       -- 撤回范围说明，留痕不删除
);

-- 跨机构映射关系，两侧各为一家机构。
CREATE TABLE IF NOT EXISTS mappings (
    mapping_id TEXT PRIMARY KEY,
    term_a TEXT NOT NULL REFERENCES term_submissions(term_id),
    term_b TEXT NOT NULL REFERENCES term_submissions(term_id),
    institution_a TEXT NOT NULL,
    institution_b TEXT NOT NULL,
    current_version INTEGER,                    -- 当前生效版本号，未生效为 NULL
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- 映射版本：翻译被推翻时新增版本，旧版本置为 superseded 但永不删除。
CREATE TABLE IF NOT EXISTS mapping_versions (
    mapping_id TEXT NOT NULL REFERENCES mappings(mapping_id),
    version_no INTEGER NOT NULL,
    relation_type TEXT NOT NULL,                -- equivalent | narrower | broader | incomparable
    ambiguity_note TEXT NOT NULL DEFAULT '',    -- 歧义说明，随版本修订
    change_note TEXT NOT NULL DEFAULT '',       -- 修订原因（如翻译被推翻）
    status TEXT NOT NULL DEFAULT 'pending',     -- pending | effective | superseded | rejected
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    effective_at TEXT,
    PRIMARY KEY (mapping_id, version_no)
);

-- 双边临床负责人签署记录，只增不改。
CREATE TABLE IF NOT EXISTS mapping_confirmations (
    mapping_id TEXT NOT NULL,
    version_no INTEGER NOT NULL,
    institution_ref TEXT NOT NULL,
    decision TEXT NOT NULL,                     -- confirm | reject
    signed_by TEXT NOT NULL,
    signed_at TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (mapping_id, version_no, institution_ref)
);

-- 向指定协作任务发放的指标目录，发放后不可变。
CREATE TABLE IF NOT EXISTS catalogs (
    catalog_id TEXT PRIMARY KEY,
    task_ref TEXT NOT NULL,
    issued_by TEXT NOT NULL,
    issued_at TEXT NOT NULL
);

-- 目录条目绑定发放时的生效映射版本；版本日后被取代不影响本记录。
CREATE TABLE IF NOT EXISTS catalog_entries (
    catalog_id TEXT NOT NULL REFERENCES catalogs(catalog_id),
    indicator_key TEXT NOT NULL,
    indicator_ref TEXT NOT NULL UNIQUE,         -- 全局引用：catalog_id/indicator_key
    mapping_id TEXT NOT NULL,
    version_no INTEGER NOT NULL,
    PRIMARY KEY (catalog_id, indicator_key)
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES ('002_terminology');
