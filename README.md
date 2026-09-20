# 跨境医疗协作任务授权交换

联盟机构分别管理本地医疗数据；协作控制信息只涉及机构身份、执行许可、数据摘要及回执。

本服务采用 HTTP 接口和 SQLite 本地文件。运行参数 `PORT` 指定监听端口，`DATABASE_PATH` 指定数据文件；`fixtures/example.json` 保存不含真实身份的交换示例，`contracts/entities.json` 记录字段约定，`docs/domain.md` 介绍来源与范围。

## 跨机构术语与用途协议

针对各机构护理指标与病种术语表述不一致的问题，服务提供术语与用途的协议登记、双方确认、目录发放与监管溯源。中心只保存字段定义的不可逆摘要与授权元数据：不接收原始病历，不执行模型训练。

调用方通过请求头表明身份：`X-Actor-Institution`（机构编号）与 `X-Actor-Role`（`data-steward` / `clinical-lead` / `lead-center` / `regulator`）。

| 接口 | 角色 | 用途 |
| --- | --- | --- |
| `POST /term-submissions` | data-steward | 提交本地字段定义的不可逆摘要、语言、单位与授权用途 |
| `GET /term-submissions` | 各机构 | 查看本机构已提交的字段摘要 |
| `POST /mappings` | data-steward | 就两份不同机构的提交发起映射（第 1 版） |
| `POST /mappings/{ref}/versions` | data-steward | 在同一映射下追加修订版本 |
| `GET /mappings/{ref}` | 当事方 | 查看映射及全部版本状态 |
| `POST /mappings/{ref}/versions/{n}/confirmations` | clinical-lead | 签署该版本；双方签署齐全后生效 |
| `POST /mappings/{ref}/versions/{n}/overturn` | clinical-lead | 推翻该版本翻译（历史保留） |
| `POST /mappings/{ref}/versions/{n}/withdrawals` | clinical-lead | 登记撤回及范围（任务/用途/时间） |
| `POST /mappings/{ref}/versions/{n}/ambiguity-notes` | 当事方 | 登记该版本的歧义说明 |
| `POST /tasks/{task}/catalog-grants` | lead-center | 把生效版本发放到指定协作任务 |
| `GET /tasks/{task}/catalog` | 合作方 | 查询本机构获授权的映射与歧义说明 |
| `GET /indicators/{key}/lineage` | regulator | 沿已发布指标溯源签署与撤回范围 |

推翻与撤回只追加记录：使用过旧版本的历史结果可沿 `(indicator_key, version)` 永久回查。

## 本地开发

服务启动时会自动应用 `migrations/` 下未执行的迁移；`make migrate` 可在需要时用 sqlite3 命令行手工初始化。`make test` 运行现有自动化检查，`make run` 启动服务。`docker compose up --build` 可以启动隔离容器，`APP_PORT` 可调整宿主机端口。
