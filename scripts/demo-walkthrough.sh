#!/usr/bin/env bash
# api-ops demo 走查脚本
# 前置：docker compose up -d 后等 ~30s，端口 8088 监听
# 用法：bash scripts/demo-walkthrough.sh

set -u

API="http://localhost:8088"
if command -v jq >/dev/null 2>&1; then
  JQ="jq"
else
  JQ="cat"
  echo "(未装 jq，原始 JSON 输出)"
fi

START=$(date -v-7d +%s 2>/dev/null || date -d "7 days ago" +%s)
END=$(date +%s)

pass() { echo "✓ 场景 $1 通过"; }
fail() { echo "✗ 场景 $1 失败：$2"; }

echo "============================================="
echo "  api-ops Phase2 demo 走查"
echo "  $(date)"
echo "============================================="

# 场景 1：Dashboard KPI
echo
echo "--- 场景 1：Dashboard 今日 KPI ---"
R=$(curl -s "$API/api/dashboard/today")
if echo "$R" | grep -q '"request_count"'; then
  REQ=$(echo "$R" | grep -oE '"request_count":[0-9]+' | head -1 | cut -d: -f2)
  REV=$(echo "$R" | grep -oE '"revenue_usd":[0-9.]+' | head -1 | cut -d: -f2)
  echo "今日调用：$REQ 次 / 收入 \$$REV"
  pass "1"
else
  fail "1" "$R"
fi

# 场景 2：客户对账 vip_acme
echo
echo "--- 场景 2：客户对账 vip_acme (user_id=1) ---"
R=$(curl -s "$API/api/billing/customer/1/preview?start=$START&end=$END")
if echo "$R" | grep -q '"vip_acme"'; then
  USER=$(echo "$R" | grep -oE '"username":"[^"]+"' | head -1 | cut -d'"' -f4)
  PROFIT=$(echo "$R" | grep -oE '"profit_rate":[0-9.]+' | head -1 | cut -d: -f2)
  echo "客户：$USER / 利润率：$(echo "$PROFIT * 100" | bc -l 2>/dev/null || echo $PROFIT)%"
  pass "2"
else
  fail "2" "$R"
fi

# 场景 3：上游对账 openai-azure
echo
echo "--- 场景 3：上游对账 openai-azure ---"
R=$(curl -s "$API/api/upstream-pricing?vendor=openai-azure")
if echo "$R" | grep -q '"openai-azure"'; then
  N=$(echo "$R" | grep -oE '"model_name"' | wc -l | tr -d ' ')
  echo "openai-azure 价目：$N 个 model"
  pass "3"
else
  fail "3" "$R"
fi

# 场景 4：渠道健康
echo
echo "--- 场景 4：渠道健康 ---"
R=$(curl -s "$API/api/upstream/channels")
if echo "$R" | grep -q '"balance"'; then
  N=$(echo "$R" | grep -oE '"id":' | wc -l | tr -d ' ')
  echo "在监控渠道：$N 个"
  pass "4"
else
  fail "4" "$R"
fi

# 场景 5：供应商列表
echo
echo "--- 场景 5：供应商列表 ---"
R=$(curl -s "$API/api/vendors")
if echo "$R" | grep -q '"code":"'; then
  N=$(echo "$R" | grep -oE '"code":"' | wc -l | tr -d ' ')
  echo "供应商：$N 个"
  pass "5"
else
  fail "5" "$R"
fi

echo
echo "============================================="
echo "  走查完成"
echo "============================================="
