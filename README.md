# 跨境医疗协作任务授权交换

联盟机构分别管理本地医疗数据；协作控制信息只涉及机构身份、执行许可、数据摘要及回执。

本服务采用 HTTP 接口和 SQLite 本地文件。运行参数 `PORT` 指定监听端口，`DATABASE_PATH` 指定数据文件；`fixtures/example.json` 保存不含真实身份的交换示例，`contracts/entities.json` 记录字段约定，`docs/domain.md` 介绍来源与范围。

## 跨机构术语与用途协议

中心不接收原始病历，也不执行模型训练。各机构提交本地字段定义的不可逆摘要、适用语言、单位及授权用途；映射版本经两侧临床负责人确认生效后，才能向指定协作任务发放指标目录。术语翻译被推翻时新增版本，使用过旧版本的历史结果保留不删。

| 接口 | 说明 |
| --- | --- |
| `POST /terms` | 提交术语定义摘要（`submitter`/`clinical_lead`） |
| `GET /terms/{term_id}` | 查看术语提交（提交机构、映射对侧、监管） |
| `POST /terms/{term_id}/withdraw` | 撤回术语，须说明撤回范围 |
| `POST /mappings` | 提议映射，创建待确认版本 |
| `GET /mappings/{mapping_id}` | 查看映射全部版本与签署 |
| `POST /mappings/{mapping_id}/versions` | 提议新版本（如翻译被推翻） |
| `POST /mappings/{mapping_id}/versions/{n}/confirm` | 临床负责人确认，双边齐全即生效 |
| `POST /mappings/{mapping_id}/versions/{n}/reject` | 临床负责人否决 |
| `POST /catalogs` | 向指定协作任务发放指标目录（`clinical_lead`） |
| `GET /catalogs/{catalog_id}` | 查看目录 |
| `GET /tasks/{task_ref}/catalog` | 合作方查询获授权的映射与歧义说明 |
| `GET /catalogs/{catalog_id}/entries/{key}/lineage` | 监管追溯签署与撤回范围 |

请求须携带身份头 `X-Institution-Ref`、`X-Actor-Ref`、`X-Actor-Role`（骨架实现，取值见契约）。服务启动时自动应用 `migrations/` 下的全部迁移。

## 本地开发

`make migrate` 初始化数据文件，`make test` 运行现有自动化检查，`make run` 启动服务。`docker compose up --build` 可以启动隔离容器，`APP_PORT` 可调整宿主机端口。
