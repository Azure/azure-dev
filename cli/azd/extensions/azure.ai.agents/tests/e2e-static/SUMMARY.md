# E2E Static Test Framework — 测试总结

## 概述

为 `azd ai agent` CLI 扩展构建的全自动、确定性 E2E 测试框架。通过 **tmux** 驱动交互式
CLI prompt，无 LLM 参与，模拟人类操作：选择菜单项、输入文本、按回车 — 验证完整的
init → provision → deploy → invoke → teardown 生命周期。

## 最新测试结果 (2026-06-18)

### Tier 0: 离线验证 (16/16 PASS)
```
纯 CLI 参数/帮助测试，无需 Azure 连接，~15s
```

### Tier 1: Init 交互变体 (8/8 PASS)
```
测试各种语言/模板组合的 init 交互，需要 Azure 认证，~3min
```

### Tier 2: 完整 Golden Path (5/5 PASS)
```
✓ init:        PASS  — 14 interactive steps
✓ provision:   PASS  — 2m57s (RG, AI Account, Project, Model, ACR)
✓ deploy:      PASS  — 4m12s (ACR build + push + agent activation)
✓ invoke:      PASS  — "Hello! 2 + 2 equals 4."
✓ teardown:    PASS  — 3m22s (RG delete + purge)
Total elapsed: ~13 min
```

## 前置条件

### 软件环境
| 组件 | 要求 | 验证命令 |
|------|------|----------|
| WSL | 需要 bash | `wsl --status` |
| tmux | >=3.4 | `tmux -V` |
| azd | >=1.25.5 | `azd version` |
| azure.ai.agents 扩展 | >=0.1.38-preview | `azd extension list` |
| Python | >=3.12 | `python3 --version` |
| az CLI | 可从 WSL 访问 | `az version` |
| gh CLI | 可从 Windows 访问 | `gh.exe auth token` |

### Azure 认证（关键！）

**必须使用 azd 内置认证，不能用 az CLI 代理认证：**

```bash
# 在 WSL 中执行：
azd auth login                                    # 登录
azd config set auth.useAzCliAuth false            # 确保不走 az CLI
azd config show | grep useAzCliAuth               # 验证: "false"
```

> ⚠️ 如果 `auth.useAzCliAuth` 为 `true`，azd 会调 `az account get-access-token`，
> 在 WSL 中需要 ~10s（跨 WSL 边界调 Windows az CLI），超过 Go SDK 10s timeout →
> `signal: killed`。deploy、invoke、teardown 全部失败。

### Azure 订阅
- 订阅需要有足够的模型配额（gpt-4.1-mini，Standard SKU）
- 推荐 region: **eastus2**（配额充足，已验证）

## 运行测试

### 方式一：从 PowerShell 调用 WSL 运行（推荐）

```powershell
# Tier 0 (离线，~15s)
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:/usr/bin:/bin"
cd /path/to/azure-dev/cli/azd/extensions/azure.ai.agents/tests/e2e-static
python3 test_tier0.py
'

# Tier 1 (init 变体，~3min)
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:/usr/bin:/bin"
cd /path/to/azure-dev/cli/azd/extensions/azure.ai.agents/tests/e2e-static
python3 test_tier1.py
'

# Tier 2 (完整 golden path，~13min)
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:/usr/bin:/bin"
export E2E_HOME="$HOME"
export E2E_CREATE_PROJECT=true
export E2E_LOCATION=eastus2
export E2E_DEPLOY_MODE=code
cd /path/to/azure-dev/cli/azd/extensions/azure.ai.agents/tests/e2e-static
python3 test_full_e2e.py
'
```

### 方式二：从 WSL 内部运行

```bash
export PATH="$HOME/bin:/usr/local/bin:/usr/bin:/bin"
export E2E_HOME="$HOME"
export E2E_CREATE_PROJECT=true
export E2E_LOCATION=eastus2
export E2E_DEPLOY_MODE=code
cd /path/to/azure-dev/cli/azd/extensions/azure.ai.agents/tests/e2e-static

# 全部 tier
python3 test_tier0.py && python3 test_tier1.py && python3 test_full_e2e.py

# 或使用 run_all.sh
bash run_all.sh
```

### 方式三：保留部署不清理（用于调试）

```bash
python3 test_full_e2e.py --keep    # 跳过 teardown，agent 保持部署
```

## 环境变量参考

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `E2E_HOME` | `$HOME` | azd/az 配置目录 |
| `E2E_CREATE_PROJECT` | `false` | **建议 `true`**: 每次新建 Foundry 项目，避免依赖已有资源 |
| `E2E_LOCATION` | `eastus2` | Azure region（建议 eastus2，配额充足） |
| `E2E_DEPLOY_MODE` | `code` | 部署模式：`code`（源码）或 `container`（容器） |
| `E2E_PROJECT` | (空) | 已有 Foundry 项目名（仅 `CREATE_PROJECT=false` 时用） |
| `E2E_SUBSCRIPTION` | (空) | 订阅 ID（为空时用 azd 默认） |
| `E2E_TENANT` | (空) | AAD 租户 ID |
| `E2E_AGENT_NAME` | (空) | 自定义 agent 名（并行隔离用） |
| `E2E_TMUX` | `tmux` | tmux 路径 |
| `E2E_TESTDIR` | `/tmp/e2e-tests/full-e2e` | 测试工作目录 |

## Tier 2 Init 交互步骤（Create New Project 模式）

| 步骤 | Prompt | 动作 |
|------|--------|------|
| 1 | Select a language | 过滤 "Python" + Enter |
| 2 | Select a starter template | 过滤 "Basic" + Enter |
| 3 | Enter a name | Enter（默认名） |
| 4 | Select a Foundry project | 选 "Create a new Foundry project" |
| 7 | Select subscription | Enter（接受默认） |
| 8 | Select location | 过滤 `E2E_LOCATION` + Enter |
| 9 | Model specified in manifest | Enter（使用 gpt-4.1-mini） |
| 10 | Select version | Enter（默认版本） |
| 11 | Select SKU | Enter（Standard） |
| 12 | Enter capacity | Enter（默认 10） |
| 13 | Enter deployment name | Enter（默认 = 模型名） |
| 14 | 完成: "agent definition added to your azd project" |

## Invoke 注意事项

invocations 协议 **必须** 用 `--input-file` 发送 JSON payload：

```bash
# ✅ 正确（用 --input-file）
echo '{"message": "what is 2+2?"}' > payload.json
azd ai agent invoke <service> --new-session -f payload.json

# ❌ 错误（positional message 对 invocations 协议发空 body → HTTP 500）
azd ai agent invoke <service> "what is 2+2?"
```

这是 azd CLI 的已知行为：positional message 只对 `responses` 协议有效，
对 `invocations` 协议会发送空 body，导致 agent 端 `json.JSONDecodeError`。

## 架构

```
┌──────────────────────────────────────────────────────┐
│  test_full_e2e.py (Python test driver)               │
│                                                      │
│  ┌────────┐  send-keys     ┌─────────────────────┐  │
│  │ tmux   │ ─────────────► │ azd ai agent init   │  │
│  │session │ ◄───────────── │ provision / deploy   │  │
│  └────────┘  capture-pane  │ invoke / down        │  │
│                             └─────────────────────┘  │
│  Sentinel: __DONE_{pid}_{counter}_$?                 │
│  每次 run_cmd 用唯一 sentinel 防止旧输出误匹配      │
└──────────────────────────────────────────────────────┘
```

## 常见问题与排错指南

### 1. `signal: killed` — azd 调用 az CLI 超时

**症状：** deploy、invoke 或 teardown 输出 `signal: killed`，或 `AzureCLICredential` 相关错误。

**根因：** `azd config` 中 `auth.useAzCliAuth=true` 导致 azd 每次鉴权都调
`az account get-access-token`。在 WSL 中 az CLI wrapper 需要调 Windows az CLI
（`/mnt/c/Program Files (x86)/Microsoft SDKs/Azure/CLI2/wbin/az`），
每次需 ~10s，超过 Go SDK 的 10s timeout。

**解决：**
```bash
azd auth login                           # 用 azd 内置认证
azd config set auth.useAzCliAuth false   # 关闭 az CLI 代理
```

### 2. Model prompt 无限循环

**症状：** init 阶段 `? model 'gpt-4.1-mini' is specified in the agent manifest` 反复出现 25+ 次，最终 `FAIL (too many steps)`。

**根因：** gpt-4.1-mini 在目标 region 没有可用配额（可能被之前的部署占用了），
CLI 反复回到同一个 prompt。

**解决：**
1. 先清理之前的部署：`az group delete --name <旧RG名> --yes`
2. 等待删除完成后再运行测试
3. 代码中已有循环检测（3次重复后自动尝试替代选项）
4. 使用 `E2E_CREATE_PROJECT=true` 每次新建，避免与旧资源冲突

### 3. Invoke 返回 HTTP 500

**症状：** `azd ai agent invoke "message"` 返回 `500 Internal Server Error`。

**根因有两种：**

**(a) 空 body 问题：** positional message 参数对 invocations 协议发送空 body，
agent 端报 `JSONDecodeError: Expecting value: line 1 column 1 (char 0)`。

**解决：** 改用 `--input-file`：
```bash
echo '{"message": "what is 2+2?"}' > payload.json
azd ai agent invoke <service> --new-session -f payload.json
```

**(b) Agent 刚部署还没 ready：** container 启动需要 30-60s。

**解决：** 测试中已有 30s（code模式）/ 60s（container模式）等待 + 3次重试。

### 4. Invoke 解析器报 "empty response" 但实际有输出

**症状：** tmux 捕获的输出中可以看到正确的 agent 响应（如 `"response": "2+2=4"`），
但测试报 `WARNING: invoke returned empty response`。

**根因：** 响应提取逻辑从第一个匹配 `invoke` + service_name 的行开始扫描，
但 tmux pane 中包含前面 deploy 阶段的输出（也含 "invoke" 字样），
导致提取器匹配到 deploy 阶段的行 → 遇到 deploy 的 sentinel 就 break → 响应为空。

**解决：** 从 capture 的 **最后一个** invoke 命令行开始扫描（已修复）。

### 5. Provision 报 `InvalidResourceGroupLocation`

**症状：** `The resource group is in location 'xxx' but deployment specifies 'yyy'`。

**根因：** 使用已有 Foundry project 时，azd 部署到 project 所在的 resource group，
如果 RG location 和部署 location 不一致就报错。

**解决：** 使用 `E2E_CREATE_PROJECT=true`，azd 会自动创建新 RG，location 一致。

### 6. Provision 报 `ScopeLocked`

**症状：** teardown 时 `The scope is locked and deletion is not allowed`。

**根因：** 已有 resource group 上有 Azure lock。

**解决：** 使用 `E2E_CREATE_PROJECT=true`，新建的 RG 没有 lock。

### 7. Deploy 成功但 exit code 1

**症状：** agent 已成功部署并激活（`azd ai agent show` 显示 `active`），
但 `azd deploy` 返回 exit code 1。

**根因：** postdeploy lifecycle hook 需要调 API 获取 agent 版本信息，
这个请求也需要认证。如果认证有问题（如 useAzCliAuth=true），
hook 失败导致整体 exit code 1，但 deploy 本身是成功的。

**解决：** 确保 `auth.useAzCliAuth=false`，postdeploy hook 就能正常执行。

### 8. `select_by_text` 过滤后选错项

**症状：** 输入过滤文本后 Enter，但选中了错误的项。

**根因：** 过滤器 UI 更新有延迟（尤其是 Foundry project 列表需要 ~3s）。

**解决：** `select_by_text` 的 `delay` 参数可调，默认 1.5s，project picker 用 3s。

### 9. GitHub token 问题

**症状：** `azd ai agent init` 在下载模板时报 `rate limit` 或 401。

**根因：** GitHub API 匿名请求有 rate limit。

**解决：** 确保 Windows 上 `gh.exe auth status` 正常，测试会自动调
`gh.exe auth token` 获取 token 并设为 `$GITHUB_TOKEN`。

### 10. WSL tmux 找不到

**症状：** `tmux: command not found`。

**解决：**
```bash
# 检查 tmux 位置
which tmux
# 如果不在 PATH 中，设置环境变量
export E2E_TMUX=/usr/local/bin/tmux
```

## 文件清单

| 文件 | 用途 |
|------|------|
| `test_full_e2e.py` | **Tier 2 主测试** — init→provision→deploy→invoke→teardown |
| `test_tier0.py` | **Tier 0** — 16 个离线 CLI 验证测试 |
| `test_tier1.py` | **Tier 1** — 8 个 init 交互变体测试 |
| `test_tier2.py` | Tier 2 编排器（并行 code + container） |
| `run_all.sh` | 全 tier 运行脚本 |
| `SUMMARY.md` | 本文档 |
| `README.md` | 框架原始说明 |
| `pipeline.yml` | GitHub Actions workflow |

## CI Pipeline 设计

| 方面 | 本地 WSL | GitHub Runner |
|------|----------|---------------|
| 认证 | `azd auth login` (内置) | OIDC: `azd auth login --federated-credential-provider github` |
| az CLI | 慢（跨 WSL）→ 不使用 | 快（原生）→ 可选 |
| GitHub token | `gh.exe auth token` | `${{ github.token }}` |
| tmux | `/usr/local/bin/tmux` | `/usr/bin/tmux` |
| Region | eastus2 | 配置为 pipeline var |
| Foundry | 每次新建 | 每次新建 |
