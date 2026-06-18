# 本地测试指南 (Local Test Guide)

> **最后验证通过：2026-06-18，所有 Tier 全部 PASS**
>
> 按照本文档步骤操作即可复现。如遇问题参考文末「常见问题」。

---

## 第一步：检查前置条件

在 **PowerShell** 中执行：

```powershell
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:$HOME/.pyenv/versions/3.12.3/bin:/usr/bin:/bin"
echo "tmux:    $(tmux -V)"
echo "python3: $(python3 --version 2>&1)"
echo "azd:     $(azd version 2>&1 | head -1)"
echo "ext:     $(azd extension list 2>&1 | grep azure.ai.agents | head -1)"
echo "gh.exe:  $(/mnt/c/Program\ Files/GitHub\ CLI/gh.exe --version 2>&1 | head -1)"
'
```

预期输出（版本号可能更高）：
```
tmux:    tmux 3.4
python3: Python 3.12.3
azd:     azd version 1.25.5
ext:     azure.ai.agents            Foundry agents (Preview)               0.1.38-preview
gh.exe:  gh version 2.85.0
```

如果缺少任何组件，先安装再继续。

---

## 第二步：确保 azd 认证正确

在 **PowerShell** 中执行：

```powershell
wsl -e bash --norc --noprofile -c 'export PATH="$HOME/bin:/usr/local/bin:/usr/bin:/bin" && azd auth login'
```

登录成功后，确认 **不使用 az CLI 代理认证**：

```powershell
wsl -e bash --norc --noprofile -c 'export PATH="$HOME/bin:/usr/local/bin:/usr/bin:/bin" && azd config unset auth.useAzCliAuth 2>/dev/null; echo OK'
```

> ⚠️ **关键：** 如果 `auth.useAzCliAuth=true`，azd 会调 az CLI 获取 token，
> 在 WSL 中跨进程调用 Windows az CLI 需要 ~10 秒，超过 Go SDK 10 秒 timeout，
> 导致 deploy/invoke/teardown 全部报 `signal: killed`。

---

## 第三步：运行测试

### Tier 0 — 离线验证（~15 秒，16 个测试）

无需 Azure 连接，纯 CLI 参数/帮助文本测试。

```powershell
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:$HOME/.pyenv/versions/3.12.3/bin:/usr/bin:/bin"
cd /mnt/d/jwshare/adc-hosted-agent/azdcli-e2e-testgates/e2e-static-tests2
python3 test_tier0.py
'
```

预期：`16 passed, 0 failed`

### Tier 1 — Init 交互变体（~3 分钟，8 个测试）

测试各语言/模板组合的 `azd ai agent init` 交互流程（不执行 provision）。

```powershell
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:$HOME/.pyenv/versions/3.12.3/bin:/usr/bin:/bin"
cd /mnt/d/jwshare/adc-hosted-agent/azdcli-e2e-testgates/e2e-static-tests2
python3 test_tier1.py
'
```

预期：`8 passed, 0 failed`

### Tier 2 — 完整 Golden Path（~13 分钟）

完整生命周期：init → provision → deploy → invoke → teardown。

**每次新建 Foundry 项目**，不依赖任何已有 Azure 资源。

```powershell
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:$HOME/.pyenv/versions/3.12.3/bin:/usr/bin:/bin"
export E2E_HOME="$HOME"
export E2E_CREATE_PROJECT=true
export E2E_LOCATION=eastus2
export E2E_DEPLOY_MODE=code
cd /mnt/d/jwshare/adc-hosted-agent/azdcli-e2e-testgates/e2e-static-tests2
python3 test_full_e2e.py
'
```

预期：
```
✓ init:        PASS
✓ provision:   PASS
✓ deploy:      PASS
✓ invoke:      PASS
✓ teardown:    PASS

✓ ALL REQUIRED PHASES PASSED
```

### 全量运行（Tier 0 + 1 + 2）

> **注意：** 各 Tier 之间用 `;` 分隔（不要用 `&&`），这样即使某个 Tier
> 有偶发失败也不会阻止后续 Tier 运行。Tier 1 的 existing-project 测试
> 对 Azure API 响应速度敏感，偶尔会 flaky。

```powershell
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:$HOME/.pyenv/versions/3.12.3/bin:/usr/bin:/bin"
export E2E_HOME="$HOME"
export E2E_CREATE_PROJECT=true
export E2E_LOCATION=eastus2
export E2E_DEPLOY_MODE=code
cd /mnt/d/jwshare/adc-hosted-agent/azdcli-e2e-testgates/e2e-static-tests2
python3 test_tier0.py; python3 test_tier1.py; python3 test_full_e2e.py
'
```

---

## 第四步（可选）：保留部署用于调试

如果想让 agent 保持部署状态，便于手动调用或在 Portal 中查看：

```powershell
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:$HOME/.pyenv/versions/3.12.3/bin:/usr/bin:/bin"
export E2E_HOME="$HOME"
export E2E_CREATE_PROJECT=true
export E2E_LOCATION=eastus2
cd /mnt/d/jwshare/adc-hosted-agent/azdcli-e2e-testgates/e2e-static-tests2
python3 test_full_e2e.py --keep
'
```

用完后手动清理：
```powershell
wsl -e bash --norc --noprofile -c '
export PATH="$HOME/bin:/usr/local/bin:/usr/bin:/bin"
az group list --query "[?starts_with(name, '"'"'rg-agent-framework'"'"')].name" -o tsv
'
# 找到 RG 名后删除：
wsl -e bash --norc --noprofile -c "export PATH=\$HOME/bin:/usr/local/bin:/usr/bin:/bin && az group delete --name <RG名> --yes --no-wait"
```

---

## 环境变量参考

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `E2E_CREATE_PROJECT` | `false` | **建议 `true`**：每次新建 Foundry 项目 |
| `E2E_LOCATION` | `eastus2` | Azure region，建议 eastus2（配额充足） |
| `E2E_DEPLOY_MODE` | `code` | `code`（源码部署）或 `container`（容器部署） |
| `E2E_HOME` | `$HOME` | azd/az 配置目录 |
| `E2E_TESTDIR` | `/tmp/e2e-tests/full-e2e` | 测试工作目录 |

---

## 常见问题

### 1. deploy/invoke/teardown 报 `signal: killed`

**根因：** `auth.useAzCliAuth=true`，azd 调 az CLI 获取 token，WSL 中超时被 kill。

**解决：**
```bash
azd auth login
azd config unset auth.useAzCliAuth
```

### 2. init 阶段 model prompt 无限循环

**症状：** `? model 'gpt-4.1-mini' is specified in the agent manifest` 反复出现。

**根因：** 目标 region 的 gpt-4.1-mini 没有可用配额（被旧部署占用）。

**解决：**
1. 清理旧部署：`az group delete --name <旧RG名> --yes`
2. 等待删除完成后再测试
3. 测试代码已有循环检测（3 次重复后自动尝试替代选项）

### 3. invoke 返回 HTTP 500

**根因 (a)：** positional message 对 invocations 协议发空 body。测试已改用 `--input-file`。

**根因 (b)：** Agent 刚部署还没 ready，容器启动需 30-60s。测试已有等待 + 重试。

### 4. invoke 解析器报 "empty response"

**根因：** tmux 捕获中包含前面 phase 的旧 sentinel，响应提取器匹配到旧行。

**已修复：** 从最后一个 invoke 命令行开始扫描。

### 5. provision 报 `InvalidResourceGroupLocation`

**根因：** 用已有 Foundry project，其 RG location 与部署 location 不匹配。

**解决：** 使用 `E2E_CREATE_PROJECT=true`，azd 自动创建 location 一致的新 RG。

### 6. deploy 成功但 exit code 1

**根因：** postdeploy hook 调 API 获取 agent 版本信息也需认证，若认证有问题则 hook 失败。

**解决：** 确保 `auth.useAzCliAuth` 未设置或为 false。

### 7. `select_by_text` 过滤后选错项

**根因：** 过滤器 UI 更新有延迟。

**解决：** 代码中 `select_by_text` 的 `delay` 参数可调，默认 1.5s，project picker 用 3s。

### 8. 找不到 `python3` 或版本不对

**注意：** WSL 默认 `/usr/bin/python3` 可能是 3.8，测试需要 3.12+。

**解决：** PATH 中必须包含 `$HOME/.pyenv/versions/3.12.3/bin`，如上面命令所示。

### 9. 配额不足导致 provision 失败

**症状：** 模型部署报 `InsufficientQuota` 或类似错误。

**解决：**
1. 确认之前的测试 RG 已删除（释放配额）
2. 换 region：`export E2E_LOCATION=westus3` 或 `northcentralus`
3. 在 Azure Portal 检查目标 region 的 gpt-4.1-mini Standard SKU 可用配额

### 10. teardown 报 `ScopeLocked`

**根因：** 已有 RG 有 Azure lock。

**解决：** `E2E_CREATE_PROJECT=true` 新建的 RG 没有 lock。
