# Pull Request

> 先看 [AGENTS.md] 跟 [CONTRIBUTING.md]。

## 背景 / Context

为什么需要这个改动？贴 issue 链接 / 用户反馈 / Q-decision 引用。

## 改动 / Changes

- 改了什么 / 新增什么
- 关联 RFC / AGENTS 铁律引用

## 数据源 / Data source

[3 数据源铁律] 要求新端点从 3 源之一取数：

- [ ] upstream admin API
- [ ] 直连 upstream DB (RoDB)
- [ ] 本地 cache_* 表 (5min tick)

## 测试 / Testing

- [ ] 单测已加（贴 `go test ./internal/...` 输出）
- [ ] 集成测试已加
- [ ] 公网环境验证通过（贴时间戳 + commit hash）
- [ ] 性能测试（如涉及慢查询）

## 影响 / Impact

- 性能影响（SQL 加索引？Redis 加 key？）
- 兼容性（数据库 migration？API breaking change？）
- 文档（哪些 .md 要同步更新？）

## 隐私 / Privacy

- [ ] 我没在代码 / 注释 / 截图里贴真 token / IP / 业务数据
- [ ] 我用占位符 `REPLACE_WITH_*` 替代敏感字段
- [ ] 我涂掉了所有截图里的敏感信息

## Checklist

- [ ] 我读了 [AGENTS.md]
- [ ] go build / go test / npm build 全过
- [ ] 改了 SQL 我查过 information_schema.columns
- [ ] 同步更新了 docs/（如适用）
- [ ] 提交信息遵循 [CONTRIBUTING.md §提交规范]

[AGENTS.md]: ./AGENTS.md
[CONTRIBUTING.md]: ./CONTRIBUTING.md
[3 数据源铁律]: ./docs/DESIGN.md#三大数据源铁律
