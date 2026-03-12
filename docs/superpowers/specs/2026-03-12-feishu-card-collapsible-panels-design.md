# 飞书卡片格式优化设计文档

## 目标

优化飞书流式卡片的显示效果，通过以下两个功能提升可读性：
1. **折叠面板**：将代码块转换为可折叠的面板（默认收起）
2. **标题转换**：将Markdown二级标题转换为加粗文本+技术风格emoji

## 背景

当前问题：
- 代码块直接展开显示，占用大量卡片空间
- Markdown标题（##）在飞书卡片中无法渲染为标题格式
- compact_stream模式虽然过滤了代码块，但用户仍希望能查看完整输出

## 方案选择

**方案A（选定）：在 StreamingCard 构建时统一处理**

- 在 `internal/feishu/card.go` 的所有 `StreamingCard*` 函数中统一处理
- 调用方（handler.go）无需修改
- 复用现有 `compactMode` 配置

## 架构设计

### 模块职责

```
internal/feishu/
  ├── markdown.go         # 新增：Markdown解析与转换
  ├── card.go            # 修改：StreamingCard*函数集成格式化
  └── text.go            # 保持：FilterCodeBlocks不变
```

### 处理流程

```
Handler层 (session/handler.go)
  ↓ 传入原始Markdown content
StreamingCard*/StreamingCardWithElapsed/StreamingCardWithAbort
  ↓ 内部调用
FormatMarkdownForCard(content, compactMode)
  ↓ 输出
elements []interface{} (mixed: text/collapsible_panel)
  ↓
构建最终卡片JSON
```

## 技术设计

### 数据结构

```go
// TextElement - 普通文本元素
type TextElement struct {
    Tag     string `json:"tag"`     // "markdown"
    Content string `json:"content"` // Markdown文本
}

// CollapsiblePanel - 折叠面板元素
type CollapsiblePanel struct {
    Tag      string                 `json:"tag"`      // "collapsible_panel"
    Header   map[string]interface{} `json:"header"`   // 标题
    Expanded bool                   `json:"expanded"` // 默认false
    Elements []interface{}          `json:"elements"` // 内容（markdown代码块）
}
```

### 核心算法

**FormatMarkdownForCard(content string, compactMode bool) []interface{}**

状态机解析Markdown：
1. 逐行扫描
2. 跟踪状态：inCodeBlock、codeLanguage、toolNameContext
3. 代码块开始（```）→ 记录状态
4. 代码块结束（```）→ 生成CollapsiblePanel
5. 标题行（##）→ 转换为 **emoji + text**
6. 普通行 → 累积到文本段

输出结构：
```go
[]interface{}{
    map[string]interface{}{"tag": "markdown", "content": "文本\n**🔧 标题**\n"},
    map[string]interface{}{"tag": "collapsible_panel", ...}, // 代码块
    map[string]interface{}{"tag": "markdown", "content": "继续文本"},
}
```

### Emoji映射规则

```go
var toolEmojiMap = map[string]string{
    "bash":   "🔧",
    "read":   "📖",
    "write":  "✍️",
    "edit":   "✏️",
    "grep":   "🔍",
    "glob":   "📁",
    "agent":  "🤖",
    "default": "📄",
}
```

### 工具名提取策略

1. 从代码块语言标识推断（```bash → Bash工具）
2. 从代码块前的文本提取（"正在执行 X 工具..."）
3. 回退到"代码输出"

## 实施范围

### 修改文件

- **新增**：`internal/feishu/markdown.go`（核心转换逻辑）
- **修改**：`internal/feishu/card.go`（集成格式化）
- **新增**：`internal/feishu/markdown_test.go`（单元测试）

### 影响范围

所有流式卡片自动应用：
- `StreamingCard`
- `StreamingCardWithElapsed`
- `StreamingCardWithAbort`

### 向后兼容

- 非compact模式：保持现有行为（不启用格式化）
- compact模式：启用折叠面板 + 标题转换

## 验证计划

1. **单元测试**：markdown.go 的解析逻辑
2. **集成测试**：完整卡片构建流程
3. **手动测试**：发送实际消息到飞书群

## 预期效果

- 代码块默认收起，用户点击展开查看
- 标题使用emoji增强视觉层次
- 卡片内容减少70-90%，提升可读性
