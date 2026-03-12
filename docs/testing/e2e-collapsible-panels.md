# 飞书卡片折叠面板功能 - 端到端测试指南

## 前提条件

### 1. 配置文件

确保 `config.yaml` 中的飞书配置正确：

```yaml
feishu:
  app_id: "your_app_id"
  app_secret: "your_app_secret"
  verification_token: "your_verification_token"
  encrypt_key: "your_encrypt_key"

compact_stream: true  # 必须启用才能看到折叠效果
```

### 2. 测试环境

- 飞书测试群
- 机器人已添加到群中
- 本地可访问飞书 API

## 测试步骤

### Step 1: 启动服务

在 feature 分支的 worktree 目录中启动服务：

```bash
cd /Users/bytedance/go/agentctl
git worktree list  # 查看 worktree 位置
cd <worktree-path>  # 切换到 worktree 目录

# 启动服务
go run cmd/server/main.go
```

预期输出：
```
[server] Starting server on :8080
[feishu] Initialized Feishu client
[session] Session manager started
```

### Step 2: 发送测试消息

在飞书测试群中 @机器人 发送以下测试消息：

#### 测试用例 1: 代码块折叠

```
@机器人 帮我列出当前目录下的文件
```

**预期效果**：
- Claude 回复包含 `ls` 命令的输出
- 代码块应该显示为折叠面板：
  - 标题：`🔧 Bash 输出（点击展开）`
  - 默认收起状态
  - 点击后展开显示完整代码

#### 测试用例 2: 标题转换

```
@机器人 写一个 Hello World 程序

期望回复包含：
## 实现方案
## 代码示例
## 运行方式
```

**预期效果**：
- 标题应该转换为加粗文本：
  - `## 实现方案` → **🔧 实现方案**
  - `## 代码示例` → **🔧 代码示例**
  - `## 运行方式` → **🔧 运行方式**

#### 测试用例 3: 组合场景

```
@机器人 查看 README 文件内容并解释
```

**预期效果**：
- 回复包含标题（转为加粗 emoji 文本）
- 包含代码块（折叠面板）
- 整体卡片高度大幅减少（相比旧版本）

### Step 3: 验证卡片效果

对每个测试用例验证：

✅ **代码块折叠检查**：
- [ ] 代码块显示为折叠面板
- [ ] 面板默认收起状态
- [ ] 面板标题包含 emoji 和工具名
- [ ] 点击面板可以展开/收起
- [ ] 展开后显示完整代码（包含 ``` 标记）

✅ **标题转换检查**：
- [ ] Markdown 标题（##）转换为加粗文本
- [ ] 标题前添加了 🔧 emoji
- [ ] 标题文本正确保留

✅ **整体效果检查**：
- [ ] 卡片高度明显减少（相比未启用折叠面板时）
- [ ] 内容可读性提升
- [ ] 无格式错误或渲染异常

### Step 4: 调试和修复

如果发现问题：

#### 问题 1: 代码块没有折叠

**可能原因**：
- `compact_stream` 配置未启用
- `FormatMarkdownForCard` 未被调用

**调试步骤**：
```bash
# 检查配置
cat config.yaml | grep compact_stream

# 添加日志验证
# 在 internal/feishu/markdown.go:FormatMarkdownForCard 入口添加：
log.Printf("[markdown] FormatMarkdownForCard called, compactMode=%v", compactMode)
```

#### 问题 2: 标题没有转换

**可能原因**：
- 标题正则未匹配
- 标题在代码块内

**调试步骤**：
```bash
# 添加日志
# 在 markdown.go 标题转换部分添加：
log.Printf("[markdown] Detected heading: %s", headingText)
```

#### 问题 3: 卡片格式错误

**可能原因**：
- elements 数组结构不正确
- collapsible_panel JSON 格式错误

**调试步骤**：
```bash
# 打印 elements 数组
# 在 internal/feishu/card.go 中添加：
elementsJSON, _ := json.MarshalIndent(elements, "", "  ")
log.Printf("[card] Elements: %s", elementsJSON)
```

### Step 5: 测试通过后停止服务

```bash
# 在服务终端按 Ctrl+C
# 或发送 SIGTERM 信号
kill <pid>
```

## 回归测试

在修复问题后，重新运行所有测试用例确保：
- 所有测试用例通过
- 无新引入的问题
- 旧功能未受影响

## 非 compact 模式测试

临时修改配置：

```yaml
compact_stream: false
```

重启服务，发送相同的测试消息，验证：
- 代码块不折叠（直接展开显示）
- 标题不转换（保持 Markdown 格式）
- 与旧版本行为一致

## 测试完成标准

- [ ] 所有 3 个测试用例通过
- [ ] 代码块折叠功能正常
- [ ] 标题转换功能正常
- [ ] 组合场景工作正常
- [ ] 非 compact 模式向后兼容
- [ ] 无新引入的 bug
- [ ] 性能无明显下降

## 已知限制

1. **Emoji 映射简化**：当前所有标题使用统一的 🔧 emoji，未来可优化为根据内容使用不同 emoji
2. **H1 标题不转换**：一级标题（#）不会被转换，仅处理 ## 及以上
3. **代码块语言限制**：仅支持常见编程语言（bash、python、go、json、yaml、sql），其他语言显示为"代码输出"

## 后续优化

如果测试发现问题，可考虑：
- 增强 emoji 映射规则
- 支持更多代码块语言
- 优化折叠面板标题文案
- 添加用户自定义配置选项
