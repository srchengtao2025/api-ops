## What kind of issue is this? / 这是什么类型的 issue?

- [ ] 🐛 Bug report — 提交 bug 报告
- [ ] ✨ Feature request — 提交功能请求
- [ ] 📚 Documentation — 文档改进
- [ ] ❓ Question — 使用问题
- [ ] 🔒 Security — 安全漏洞（**不要**在 public issue 写细节，发邮件 security@api-ops.dev）

---

## Bug report / Bug 报告

> 先看 [AGENTS.md § 隐私铁律]，**别贴真 token / IP / 业务数据**。

### 环境 / Environment

- api-ops commit hash: `git rev-parse HEAD`
- Go version: `go version`
- Node version: `node --version`
- PostgreSQL version: `psql --version`
- OS: (macOS 14 / Ubuntu 22.04 / Windows 11 / ...)
- Docker version: `docker --version`

### 复现步骤 / Steps to reproduce

```
1. docker compose up -d
2. 访问 http://localhost:8088/...
3. 点击 ... 按钮
4. 看到报错 ...
```

### 期望行为 / Expected

简洁描述本应该发生什么。

### 实际行为 / Actual

简洁描述实际发生了什么。

### 错误日志 / Error logs

```
（贴 docker logs api-ops-api-1 --tail 200 输出，记得涂掉敏感信息）
```

### 截图 / Screenshots

如有界面 bug，请贴截图（**先涂掉 token / IP / 客户名 / 业务数字**）。

### 影响范围 / Impact

- [ ] 完全不可用
- [ ] 主要功能受损
- [ ] 次要功能受损
- [ ] 视觉/文案小问题
