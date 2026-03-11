# 流式输出代码块过滤配置

## 功能说明

`compact_stream` 配置项用于控制流式输出卡片中是否显示代码块。启用后，所有代码块（```...```）将被替换为简短的提示文本，只保留文字说明和最终结果，使卡片更简洁易读。

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
正在执行 Read 工具...

\`\`\`
package main

import "fmt"

func main() {
    fmt.Println("Hello, World!")
}
\`\`\`

执行完成。
```

### 简洁模式（compact_stream: true）

```
正在执行 Read 工具...

[代码块 #1 已省略]

执行完成。
```

## 实现细节

- **作用范围**：仅影响流式输出卡片的显示，不影响 Claude 的实际执行和结果
- **过滤规则**：
  - 检测 Markdown 代码块标记（```）
  - 保留代码块前后的文字说明
  - 用 "[代码块 #N 已省略]" 替代代码内容
  - 保持原有的换行和间距
- **适用场景**：
  - 快速浏览执行过程，关注结果而非细节
  - 减少卡片内容量，提升加载速度
  - 移动端查看时更简洁

## 注意事项

1. 此配置不影响最终的执行结果和日志
2. 代码块仍然会被完整记录在 Claude CLI 的 session 中
3. 如需查看完整代码，可以将 `compact_stream` 设为 `false` 或直接查看 Claude 的会话历史

## 相关文件

- 配置定义：`internal/config/config.go`
- 过滤实现：`internal/feishu/text.go`
- 应用点：
  - `internal/session/handler.go` - 会话消息处理
  - `internal/router/router.go` - 直接回复消息处理
