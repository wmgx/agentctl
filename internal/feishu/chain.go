package feishu

import (
	"sync"
	"time"
)

const defaultChainCap = 1000

// ChainEntry 记录一个 sender 的引用链状态
type ChainEntry struct {
	MsgIDs     []string // 按时间顺序的消息 ID 列表
	LastActive time.Time
	Dismissed  bool // 用户选择继续私聊后为 true，不再触发升级提示
}

// ReplyChainTracker 追踪 P2P 私聊的引用链深度，使用 LRU 淘汰策略。
// 重启后内存清空，链从零计数，无需持久化。
type ReplyChainTracker struct {
	cap     int
	mu      sync.RWMutex
	entries map[string]*ChainEntry // key: senderID
	order   []string               // LRU 顺序，最近活跃的在末尾
}

func NewReplyChainTracker(cap int) *ReplyChainTracker {
	if cap <= 0 {
		cap = defaultChainCap
	}
	return &ReplyChainTracker{
		cap:     cap,
		entries: make(map[string]*ChainEntry),
	}
}

// Track 追踪一条新消息。返回当前引用链深度（1 表示独立消息，无引用）。
// 如果 parentMsgID 为空，视为新对话起点，重置该 sender 的链。
func (t *ReplyChainTracker) Track(senderID, msgID, parentMsgID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 无引用：重置链，从头开始
	if parentMsgID == "" {
		t.resetLocked(senderID)
		entry := &ChainEntry{
			MsgIDs:     []string{msgID},
			LastActive: time.Now(),
		}
		t.entries[senderID] = entry
		t.touchLocked(senderID)
		return 1
	}

	entry, exists := t.entries[senderID]
	if !exists {
		// 内存中没有该 sender 的链，先创建占位
		// 调用方若需要完整深度，应在 Track 后调用 PrependChain
		entry = &ChainEntry{
			MsgIDs:     []string{msgID},
			LastActive: time.Now(),
		}
		t.entries[senderID] = entry
		t.touchLocked(senderID)
		t.evictLocked()
		return 1
	}

	// 若 parentMsgID 不在链中（通常是 bot 回复），将其插入到当前消息之前
	// 这样 GetChain 返回的列表同时包含用户消息和 bot 回复，用于合并转发
	if !containsMsgID(entry.MsgIDs, parentMsgID) {
		entry.MsgIDs = append(entry.MsgIDs, parentMsgID)
	}
	entry.MsgIDs = append(entry.MsgIDs, msgID)
	entry.LastActive = time.Now()
	t.touchLocked(senderID)
	t.evictLocked()
	return len(entry.MsgIDs)
}

// PrependChain 将向上追溯得到的历史消息 ID 前置到链里（用于内存未命中的场景）
func (t *ReplyChainTracker) PrependChain(senderID string, ancestorIDs []string) {
	if len(ancestorIDs) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, exists := t.entries[senderID]
	if !exists {
		return
	}
	entry.MsgIDs = append(ancestorIDs, entry.MsgIDs...)
}

// GetChain 返回该 sender 链上所有消息 ID 的副本
func (t *ReplyChainTracker) GetChain(senderID string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, exists := t.entries[senderID]
	if !exists {
		return nil
	}
	result := make([]string, len(entry.MsgIDs))
	copy(result, entry.MsgIDs)
	return result
}

// Reset 清除该 sender 的链（建群后调用）
func (t *ReplyChainTracker) Reset(senderID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resetLocked(senderID)
}

// Dismiss 标记该 sender 不再触发升级提示，同时清空链
func (t *ReplyChainTracker) Dismiss(senderID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := &ChainEntry{
		LastActive: time.Now(),
		Dismissed:  true,
	}
	t.entries[senderID] = entry
	t.touchLocked(senderID)
	t.evictLocked()
}

// IsDismissed 返回该 sender 是否已选择不升级
func (t *ReplyChainTracker) IsDismissed(senderID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, exists := t.entries[senderID]
	return exists && entry.Dismissed
}

// resetLocked 清除指定 sender 的链（调用前需持有写锁）
func (t *ReplyChainTracker) resetLocked(senderID string) {
	delete(t.entries, senderID)
	for i, id := range t.order {
		if id == senderID {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
}

// touchLocked 将 senderID 移到 order 末尾（最近活跃），调用前需持有写锁
func (t *ReplyChainTracker) touchLocked(senderID string) {
	for i, id := range t.order {
		if id == senderID {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
	t.order = append(t.order, senderID)
}

// containsMsgID 检查 ids 中是否已包含 id
func containsMsgID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// evictLocked 超过 cap 时淘汰最久未活跃的 entry，调用前需持有写锁
func (t *ReplyChainTracker) evictLocked() {
	for len(t.entries) > t.cap && len(t.order) > 0 {
		oldest := t.order[0]
		t.order = t.order[1:]
		delete(t.entries, oldest)
	}
}
