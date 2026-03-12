# 流式输出简洁模式配置

## 功能说明

`compact_stream` 配置项用于启用简洁模式，让飞书卡片更加清爽易读。启用后：

1. **代码块折叠面板**（新功能 2026-03-12）：所有代码块自动转换为可折叠的交互式面板
   - 默认收起状态，减少卡片长度
   - 根据代码语言显示对应工具名和 emoji（如 🔧 Bash、🐍 Python）
   - 点击"点击展开"可查看完整代码
   - 支持语言：bash、python、go、json、yaml、sql

2. **标题格式化**（新功能 2026-03-12）：Markdown 二级标题自动转换为加粗文本 + emoji
   - `## 执行结果` → **🔧 执行结果**
   - 提升视觉层次，适配飞书卡片样式
   - 仅处理 ## 及以上级别标题（H1 标题不转换）

3. **隐藏工具执行提示**：不显示 "🔧 工具名 执行中..." 这类提示

4. **隐藏工具结果**：跳过 `tool_result` 的详细输出

**最终效果**：卡片内容减少 70%-90%，代码块可按需展开查看，标题清晰醒目，整体更加简洁易读。

## 配置方法

在 `config.json` 中添加或修改 `compact_stream` 字段：

```json
{
  "feishu": {
    "app_id": "cli_xxx",
    "app_secret": "xxx"
  },
  "anthropic": {
    "api_key": "sk-ant-xxx",
    "model": "claude-haiku-4-5-20250929"
  },
  "compact_stream": true
}
```

## 效果对比

### 默认模式（compact_stream: false）

```
我来帮你分析这个日志文件...

🔧 Bash 执行中...

🔧 Read 执行中...

\`\`\`
package main

import "fmt"

func main() {
    fmt.Println("Hello, World!")
}
\`\`\`

🔧 Grep 执行中...

分析完成，发现以下问题...
```

### 简洁模式（compact_stream: true）

```
我来帮你分析这个日志文件...

[可折叠面板：🔧 Bash - 默认收起，点击展开查看完整代码]

**🔧 执行结果**

分析完成，发现以下问题...
```

**关键差异**：
- ✅ 没有 "🔧 工具名 执行中..." 提示
- ✅ 代码块转换为可折叠面板，默认收起
- ✅ 标题自动格式化为加粗 + emoji
- ✅ 卡片内容减少 70%-90%，保留查看完整输出的能力
- ✅ 更易阅读，尤其在移动端

## 实现细节

- **作用范围**：仅影响流式输出卡片的显示，不影响 Claude 的实际执行和结果
- **格式化规则**：
  - **代码块检测**：识别 Markdown 代码块标记（```）及语言标识
  - **折叠面板转换**：将代码块转换为飞书 `collapsible_panel` 组件
    - 根据语言映射工具名和 emoji（如 bash → 🔧 Bash, python → 🐍 Python）
    - 默认展开状态设为 false（收起）
    - 标题显示为"🔧 工具名"
  - **标题转换**：Markdown ## 标题转换为 `**🔧 标题文本**`
  - **保持原有间距和换行**
- **适用场景**：
  - 快速浏览执行过程，关注结果而非细节
  - 减少卡片内容量，提升加载速度
  - 移动端查看时更简洁
  - 需要时可展开查看完整代码

## 注意事项

1. 此配置不影响最终的执行结果和日志
2. 代码块仍然会被完整记录在 Claude CLI 的 session 中
3. 折叠面板默认收起，用户可点击展开查看完整代码
4. 标题 emoji 映射目前使用统一的 🔧，未来可根据标题内容智能选择
5. 仅支持常见代码语言（bash、python、go、json、yaml、sql），其他语言显示为"未知语言"

## 相关文件

- 配置定义：`internal/config/config.go`
- 核心格式化逻辑：`internal/feishu/markdown.go`（新增）
  - `FormatMarkdownForCard()` - 主入口函数
  - `parseCodeBlocks()` - 代码块检测
  - `createCollapsiblePanel()` - 折叠面板转换
  - `formatHeadings()` - 标题格式化
  - `getToolNameFromLanguage()` - 语言到工具名映射
- 过滤实现：`internal/feishu/text.go`（原有逻辑）
- 卡片组件：`internal/feishu/card.go`
  - `StreamingCard()`
  - `StreamingCardWithElapsed()`
  - `StreamingCardWithAbort()`
  - `StreamingCardAborted()`
- 应用点：
  - `internal/session/handler.go` - 会话消息处理
  - `internal/router/router.go` - 直接回复消息处理

## 测试文件

- 单元测试：`internal/feishu/markdown_test.go`
  - 基础功能测试
  - 边界情况测试（空字符串、未闭合代码块、多连续代码块等）
- E2E 测试指南：`docs/testing/e2e-collapsible-panels.md`
