-- 跨机构术语与用途协议：只保存不可逆摘要与授权元数据，不保存原始病历。
-- 除 mapping_versions.status 的状态推进外，所有表只增不删，历史版本永久保留。

CREATE TABLE IF NOT EXISTS term_submissions (
    submission_ref   TEXT PRIMARY KEY,
    institution_ref  TEXT NOT NULL,
    local_field_ref  TEXT NOT NULL,
    definition_digest TEXT NOT NULL,
    language         TEXT NOT NULL,
    unit             TEXT NOT NULL DEFAULT '',
    purposes         TEXT NOT NULL,              -- JSON 数组，授权用途
    submitted_at     TEXT NOT NULL,
    UNIQUE (institution_ref, local_field_ref)
);

CREATE TABLE IF NOT EXISTS mappings (
    mapping_ref   TEXT PRIMARY KEY,
    indicator_key TEXT NOT NULL,
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mapping_versions (
    mapping_ref          TEXT NOT NULL,
    version              INTEGER NOT NULL,
    left_submission_ref  TEXT NOT NULL,
    right_submission_ref TEXT NOT NULL,
    equivalence_note     TEXT NOT NULL,
    status               TEXT NOT NULL,          -- pending | effective | overturned
    status_reason        TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL,
    PRIMARY KEY (mapping_ref, version)
);

CREATE TABLE IF NOT EXISTS confirmations (
    confirmation_ref TEXT PRIMARY KEY,
    mapping_ref      TEXT NOT NULL,
    version          INTEGER NOT NULL,
    institution_ref  TEXT NOT NULL,
    signatory_ref    TEXT NOT NULL,
    signed_at        TEXT NOT NULL,
    UNIQUE (mapping_ref, version, institution_ref)
);

CREATE TABLE IF NOT EXISTS withdrawals (
    withdrawal_ref  TEXT PRIMARY KEY,
    mapping_ref     TEXT NOT NULL,
    version         INTEGER NOT NULL,
    institution_ref TEXT NOT NULL,
    scope           TEXT NOT NULL,               -- JSON：{tasks: [], purposes: [], from: ""}
    reason          TEXT NOT NULL,
    withdrawn_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS catalog_grants (
    grant_ref     TEXT PRIMARY KEY,
    task_ref      TEXT NOT NULL,
    indicator_key TEXT NOT NULL,
    mapping_ref   TEXT NOT NULL,
    version       INTEGER NOT NULL,
    issued_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ambiguity_notes (
    note_ref               TEXT PRIMARY KEY,
    mapping_ref            TEXT NOT NULL,
    version                INTEGER NOT NULL,
    author_institution_ref TEXT NOT NULL,
    body                   TEXT NOT NULL,
    created_at             TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES ('002_terminology_agreement');
