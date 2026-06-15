// Package e2e: 端到端 demo 走查测试
//
// 设计：
//   - 调用 scripts/demo-walkthrough.sh（5 个场景）
//   - 解析输出断言每个场景 status=0 + 含 '✓ 场景 X 通过'
//   - 用 build tag "e2e" 控制：默认 go test ./... 不跑；跑时用：
//     go test -tags=e2e ./tests/e2e/...
//
// 前置：
//   - docker compose up -d（PG + Redis + api-ops API）
//   - API 监听 http://localhost:8088
//   - 走查脚本里 7 天前到现在的数据需存在
//
// 跳过条件（用环境变量 API_OPS_E2E_SKIP=1 跳过）：
//   - 在 CI 里默认跳过；本地手动 `go test -tags=e2e -run TestE2E` 跑
//
//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repoRoot 解析到 api-ops 仓库根目录（假设 tests/e2e/walkthrough_test.go 在 <repo>/tests/e2e/）
func repoRoot(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	// tests/e2e/ → 上两级
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("找不到 go.mod (cwd=%s, root=%s): %v", wd, root, err)
	}
	return root
}

// TestE2E_DemoWalkthrough 跑 demo-walkthrough.sh + 断言 5 个场景都通过
func TestE2E_DemoWalkthrough(t *testing.T) {
	if os.Getenv("API_OPS_E2E_SKIP") == "1" {
		t.Skip("API_OPS_E2E_SKIP=1，跳过 E2E")
	}

	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "demo-walkthrough.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("找不到 walkthrough 脚本 %s: %v", scriptPath, err)
	}

	// 检查 docker compose 是否已起 API（端口 8088）
	apiURL := os.Getenv("API_OPS_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8088"
	}

	// 启动脚本（bash，因为脚本里有 set -u 和 bash 特性）
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"API="+apiURL,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 启动脚本最长等 90s（5 个场景 + curl + sleep）
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		output := stdout.String() + "\n" + stderr.String()
		if err != nil {
			t.Logf("脚本退出码非 0（err=%v），输出:\n%s", err, output)
		}

		// 解析 5 个场景
		scenarios := []struct {
			id      int
			pattern string
		}{
			{1, "✓ 场景 1 通过"},
			{2, "✓ 场景 2 通过"},
			{3, "✓ 场景 3 通过"},
			{4, "✓ 场景 4 通过"},
			{5, "✓ 场景 5 通过"},
		}
		for _, sc := range scenarios {
			if !strings.Contains(stdout.String(), sc.pattern) {
				t.Errorf("场景 %d 未通过（缺少 %q）", sc.id, sc.pattern)
			}
		}

		// 验证失败标记不存在
		for i := 1; i <= 5; i++ {
			failPattern := fmt.Sprintf("✗ 场景 %d 失败", i)
			if strings.Contains(stdout.String(), failPattern) {
				t.Errorf("场景 %d 标记为失败: %s", i, failPattern)
			}
		}

		// 退出码非 0 也算 fail（除非显式 skip）
		if err != nil && !strings.Contains(stdout.String(), "走查完成") {
			t.Errorf("脚本未正常结束: err=%v", err)
		}

	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("walkthrough 脚本超时 90s，stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

// TestE2E_DemoWalkthrough_Output 验证输出格式（不依赖 docker，仅断言脚本 shell 解析正确）
func TestE2E_DemoWalkthrough_Output(t *testing.T) {
	// 此测试不需要真实环境：仅做 shell 语法检查 + 脚本存在性
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "demo-walkthrough.sh")

	// bash -n：仅做语法检查
	cmd := exec.Command("bash", "-n", scriptPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("脚本语法错误: %v\n%s", err, out)
	}
	t.Logf("bash -n 语法检查通过")
}
