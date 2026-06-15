# api-ops

> An operations management console for LLM API aggregation scenarios.
> Dashboard / customer billing / vendor billing / profit analysis / monitoring & alerts —
> all built on the **3 data-source rule + 5 capabilities** principle.

> 中文版见 [README.md](./README.md)

---

## Use case

You run a bunch of LLM API channels (OpenAI / Anthropic / Chinese cloud vendors / self-hosted proxies),
resell tokens to your customers, and want to:

- Track daily per-customer usage, payments, cost, and gross margin
- Reconcile monthly with upstream vendors (how much you owe each)
- Monitor channel error rate, latency, stability
- Get alerts + AI-driven root-cause analysis on anomalies

**api-ops does exactly that.** Single-tenant, on-prem, sub-10-person ops team, monthly reconciliation precision.

---

## Architecture

```
              ┌─────────────────┐
              │  upstream LLM   │  (e.g. OpenAI / Anthropic / self-hosted)
              │  vendor         │
              └────────┬────────┘
                       │ call records
                       ▼
              ┌─────────────────┐
              │  upstream DB    │  (read-only account, RoDB)
              └────────┬────────┘
                       │
                       ▼
   ┌──────────────────────────────────────┐
   │           api-ops (this project)     │
   │                                      │
   │  data sources (3 rules):             │
   │   1. upstream admin API              │ real-time lists (≤ 1000 rows)
   │   2. direct upstream DB (RoDB)       │ large aggregate SQL
   │   3. local cache DB (5min tick)      │ near-real-time dashboard
   │                                      │
   │  5 capabilities:                     │
   │   - Dashboard (today / 7d trend)     │
   │   - BILLING v2 customer billing      │
   │   - BILLING v3 vendor billing       │
   │   - BILLING v4 profit analysis      │
   │   - Monitoring (channel health+alert)│
   │                                      │
   │  stack:                              │
   │   - Go 1.22 + Gin + GORM             │
   │   - PostgreSQL 15                    │
   │   - Redis 7 (optional)               │
   │   - React 18 + Antd 5 + ECharts 5    │
   │   - Feishu webhook alerts            │
   └──────────────────────────────────────┘
                       │
                       ▼
              ┌─────────────────┐
              │  Web frontend   │  http://your-server:8088/
              │  (dist baked    │  → admin login → 6 SPA pages
              │   into api img) │
              └─────────────────┘
```

---

## 3 data-source rule

**api-ops is strictly limited to 3 data sources.** Any "4th source" must be removed or archived under `archive/`.

| Source | Path | Latency | When to use |
|---|---|---|---|
| upstream API | upstream admin `/api/*` | real-time | lists / details (≤ 1000 rows) |
| direct DB (RoDB) | upstream RDS `*.logs` | real-time | large aggregate SQL |
| local cache DB | self-owned `cache_*` tables | near-real-time (5min) | dashboard live panels |

Every endpoint across the 5 capabilities must read from one of these 3. **No mocks, no shadow tables, no ad-hoc import paths.**

See [docs/DATA-SOURCES.md](./docs/DATA-SOURCES.md) for the full handler × data-source matrix (30 active endpoints).

---

## Quick start

### Prerequisites

- Go 1.22+ ([macOS 1.22.5 has an LC_UUID bug](docs/test-reports/) → run `go test` inside a container)
- Node 20+ / npm
- PostgreSQL 15
- Redis 7 (optional, degrades to no-cache mode)
- Docker + docker compose
- An upstream LLM API instance + its admin token + a read-only DB account

### Demo with docker compose

```bash
# 1. Copy .env templates
cp .env.example .env
cp .env.production.example .env.production

# 2. Edit .env, fill in 3 required placeholders (placeholders are `PLEASE_FILL_*` / `change_me` / `sk-xxx`):
#    - API_OPS_ADMIN_TOKEN=PLEASE_FILL_ADMIN_TOKEN
#    - API_OPS_RO_DSN=...password=PLEASE_FILL_PASSWORD
#    - OPS_DB_DSN=...password=change_me
#    See .env.example / .env.production.example for inline comments

# 3. Start (PG / Redis / api containers included)
docker compose up -d

# 4. Open
# http://localhost:8088/
# First boot prompts you to set the bootstrap admin password (OPS_ADMIN_BOOTSTRAP_PASSWORD)
```

### Production deploy

```bash
# 1. Cross-platform image build (macOS arm64 → remote linux/amd64)
docker buildx build --platform linux/amd64 -t api-ops:latest . --load

# 2. Push image to ECS
docker save api-ops:latest | gzip > /tmp/api-ops.tar.gz
scp /tmp/api-ops.tar.gz root@<your.server>:/tmp/

# 3. Load + docker compose on ECS
ssh root@<your.server> 'cd /opt/api-ops && \
  docker load -i /tmp/api-ops.tar.gz && \
  docker compose -f docker-compose.prod.yml up -d api'

# 4. Public exposure: nginx reverse-proxy 80 → 8088, open 80/80 inbound in cloud security group
```

See [docs/DESIGN.md §Deployment rules](./docs/DESIGN.md) and `scripts/deploy-prod.sh`.

---

## 5 capabilities

### 1. Dashboard (today / 7d trend)

- `GET /api/dashboard/today` — today's cumulative revenue / rpm / tpm
- `GET /api/dashboard/trend7d` — 7-day trend curve (**backend 5min cache, SPA 5min tick**)

### 2. BILLING v2 customer billing

- 6 endpoints: current-month summary / last-month export / task center / ZIP download / cancel
- ZIP contains README + HTML + XLSX (7-column sharedStrings header validated)
- 5 business rules (R1-R5): zero-output free / image-tagged / refunds excluded / errors excluded / unmatched upstream excluded
- 30-day task retention + auto-prune

### 3. BILLING v3 vendor billing

- 5 endpoints: vendor current-month summary / last-month export / per-vendor task / task list / ZIP download
- **Cost back-fill formula**: `cost = revenue / group_ratio × channel_vendor_map.discount`
- 5min cache tick pre-computes (round-robin: 1 (vendor, period) per tick)
- Handler reads cache first, falls back to live calc on miss

### 4. BILLING v4 profit analysis

- 1 endpoint returns summary + 30-day trend + 27 customers + 5 vendors + top10 models
- Reuses v2 revenue + v3 cost back-fill
- 4-tab SPA: trend (line) / customer (bar) / vendor (pie) / model (bar)

### 5. Monitoring center

- `GET /api/monitor/channels` — 24h business requests + independent errors + P95/P99 latency
- **New error-rate formula** (business-request denominator + independent-error numerator):
  - Denominator: `type IN (2, 5, 6)` over 24h (excludes login / recharge / admin)
  - Numerator: `type=5 AND jsonb_array_length(use_channel)=1` (excludes retry intermediate failures)
  - P95: read `channel_health_5min` bucket MAX (avoids slow `percentile_cont` on RoDB)
- Error rate ≥ 20% → channel card red glow + 1.5s pulse
- Feishu webhook alerts (10+ built-in rules + custom)

---

## Documentation

| Document | Purpose |
|---|---|
| `README.md` / `README.en.md` | This file / Chinese version |
| [AGENTS.md](./AGENTS.md) | Project iron rules (deploy / naming / field traps) |
| [docs/DESIGN.md](./docs/DESIGN.md) | 21 locked decisions + 3 data-source rule + cache tables |
| [docs/PRD-v2.md](./docs/PRD-v2.md) | Product requirements (P0-P3 + acceptance) |
| [docs/DATA-SOURCES.md](./docs/DATA-SOURCES.md) | handler × data-source matrix (30 active endpoints) |
| `docs/BILLING-v{2,3,4}-RFC.md` / `-RULES.md` | customer / vendor / profit RFC + business rules |
| [docs/SYNC-ARCHITECTURE.md](./docs/SYNC-ARCHITECTURE.md) | Data flow diagram |
| [docs/CHANGELOG.md](./docs/CHANGELOG.md) | Change history |
| [docs/test-reports/](./docs/test-reports/) | 17 deployment test reports (archived by commit hash) |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | Contributing guide (zh + en) |
| [LICENSE](./LICENSE) | MIT |

---

## Dev conventions

- **Go style**: follow `internal/`, GORM + Gin + zap-style structured log
- **3 data-source rule**: new endpoints MUST pick from API / RoDB / cache_*; mocks and shadow tables go to `archive/`
- **Business rules first**: before any PR that changes SQL, check `information_schema.columns`; do not touch the locked cost formula
- **Frontend**: React 18 + Antd 5 + ECharts 5, **JSX `{xxx}` always evaluates** (use `<task_id>` or `$(task_id)` for placeholder text)
- **Commits**: `feat(web/v4)` / `fix(billing/v3)` / `docs(P1)` / `refactor(api)` format
- **PR flow**: RFC first → PR → maintainer review → squash merge

See [CONTRIBUTING.md](./CONTRIBUTING.md).

---

## Privacy rules

> **Never** commit / post / screenshot:
> - Real tokens / API keys / SSH passwords / DB passwords
> - Real ECS IPs / RDS hosts / internal domains
> - Real customer names / channel names / model names / business numbers
> 
> Use placeholders: `REPLACE_WITH_*` / `xxx.example.com` / `provider_alpha` / `user_alpha`.

See [AGENTS.md §Privacy rules](./AGENTS.md).

---

## License

MIT — see [LICENSE](./LICENSE).

---

## Acknowledgments

- Inspired by [the upstream project](https://github.com/songquanpeng/one-api) and the LLM API proxy ecosystem
- Core contributors — see [CONTRIBUTING.md](./CONTRIBUTING.md#acknowledgments)
