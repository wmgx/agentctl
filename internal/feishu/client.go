package feishu

import (
	"context"
	"encoding/json"
	"fmt"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type Client struct {
	api     *lark.Client
	botName string
}

func NewClient(appID, appSecret, botName string) *Client {
	api := lark.NewClient(appID, appSecret,
		lark.WithLogLevel(larkcore.LogLevelInfo),
	)
	return &Client{api: api, botName: botName}
}

func (c *Client) CreateGroup(ctx context.Context, name string) (string, error) {
	req := larkim.NewCreateChatReqBuilder().
		Body(larkim.NewCreateChatReqBodyBuilder().
			Name(name).
			ChatMode("group").
			ChatType("private").
			Build()).
		Build()

	resp, err := c.api.Im.Chat.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("create chat: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("create chat failed: %s", resp.Msg)
	}
	return *resp.Data.ChatId, nil
}

func (c *Client) AddMember(ctx context.Context, chatID, userOpenID string) error {
	req := larkim.NewCreateChatMembersReqBuilder().
		ChatId(chatID).
		MemberIdType("open_id").
		Body(larkim.NewCreateChatMembersReqBodyBuilder().
			IdList([]string{userOpenID}).
			Build()).
		Build()

	resp, err := c.api.Im.ChatMembers.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("add member failed: %s", resp.Msg)
	}
	return nil
}

func (c *Client) SendText(ctx context.Context, chatID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	return c.sendMessage(ctx, chatID, "text", string(content))
}

// ReplyCard sends an interactive card as a reply to the given message ID.
func (c *Client) ReplyCard(ctx context.Context, replyToMessageID string, card interface{}) (string, error) {
	content, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshal card: %w", err)
	}
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(replyToMessageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType("interactive").
			Content(string(content)).
			Build()).
		Build()

	resp, err := c.api.Im.Message.Reply(ctx, req)
	if err != nil {
		return "", fmt.Errorf("reply card: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("reply card failed: %s", resp.Msg)
	}
	return *resp.Data.MessageId, nil
}

// ReplyText sends a text message as a reply to the given message ID.
func (c *Client) ReplyText(ctx context.Context, replyToMessageID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(replyToMessageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType("text").
			Content(string(content)).
			Build()).
		Build()

	resp, err := c.api.Im.Message.Reply(ctx, req)
	if err != nil {
		return "", fmt.Errorf("reply message: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("reply message failed: %s", resp.Msg)
	}
	return *resp.Data.MessageId, nil
}

func (c *Client) SendCard(ctx context.Context, chatID string, card interface{}) (string, error) {
	content, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshal card: %w", err)
	}
	return c.sendMessage(ctx, chatID, "interactive", string(content))
}

func (c *Client) UpdateCard(ctx context.Context, messageID string, card interface{}) error {
	content, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}

	req := larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(string(content)).
			Build()).
		Build()

	resp, err := c.api.Im.Message.Patch(ctx, req)
	if err != nil {
		return fmt.Errorf("update card: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("update card failed: %s", resp.Msg)
	}
	return nil
}

func (c *Client) AddReaction(ctx context.Context, messageID, emojiType string) (string, error) {
	req := larkim.NewCreateMessageReactionReqBuilder().
		MessageId(messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType(emojiType).Build()).
			Build()).
		Build()

	resp, err := c.api.Im.MessageReaction.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("add reaction: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("add reaction failed: %s", resp.Msg)
	}
	return *resp.Data.ReactionId, nil
}

func (c *Client) RemoveReaction(ctx context.Context, messageID, reactionID string) error {
	req := larkim.NewDeleteMessageReactionReqBuilder().
		MessageId(messageID).
		ReactionId(reactionID).
		Build()

	resp, err := c.api.Im.MessageReaction.Delete(ctx, req)
	if err != nil {
		return fmt.Errorf("remove reaction: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("remove reaction failed: %s", resp.Msg)
	}
	return nil
}

func (c *Client) sendMessage(ctx context.Context, chatID, msgType, content string) (string, error) {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build()

	resp, err := c.api.Im.Message.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("send message failed: %s", resp.Msg)
	}
	return *resp.Data.MessageId, nil
}

func (c *Client) LarkAPI() *lark.Client {
	return c.api
}

// MessageInfo 包含从飞书获取的消息基本信息，用于向上追溯引用链
type MessageInfo struct {
	MessageID  string
	ParentID   string
	SenderID   string // open_id
	SenderType string // "user" or "app"
	Text       string
}

// GetMessage 获取单条消息的基本信息，用于向上追溯引用链
func (c *Client) GetMessage(ctx context.Context, messageID string) (*MessageInfo, error) {
	req := larkim.NewGetMessageReqBuilder().
		MessageId(messageID).
		Build()

	resp, err := c.api.Im.Message.Get(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}
	if !resp.Success() {
		return nil, fmt.Errorf("get message failed: %s", resp.Msg)
	}
	if len(resp.Data.Items) == 0 {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	item := resp.Data.Items[0]
	info := &MessageInfo{
		MessageID: *item.MessageId,
	}
	if item.ParentId != nil {
		info.ParentID = *item.ParentId
	}
	if item.Sender != nil {
		if item.Sender.Id != nil {
			info.SenderID = *item.Sender.Id
		}
		if item.Sender.SenderType != nil {
			info.SenderType = *item.Sender.SenderType
		}
	}
	if item.Body != nil && item.Body.Content != nil {
		info.Text = extractText(*item.Body.Content)
	}
	return info, nil
}

// TransferOwner 将群主转移给指定用户（open_id）
func (c *Client) TransferOwner(ctx context.Context, chatID, userOpenID string) error {
	req := larkim.NewUpdateChatReqBuilder().
		ChatId(chatID).
		UserIdType("open_id").
		Body(larkim.NewUpdateChatReqBodyBuilder().
			OwnerId(userOpenID).
			Build()).
		Build()

	resp, err := c.api.Im.Chat.Update(ctx, req)
	if err != nil {
		return fmt.Errorf("transfer owner: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("transfer owner failed: %s", resp.Msg)
	}
	return nil
}

// MergeForwardMessages 将多条消息合并转发到目标群，生成「聊天记录」卡片
func (c *Client) MergeForwardMessages(ctx context.Context, messageIDs []string, toChatID string) error {
	req := larkim.NewMergeForwardMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewMergeForwardMessageReqBodyBuilder().
			ReceiveId(toChatID).
			MessageIdList(messageIDs).
			Build()).
		Build()

	resp, err := c.api.Im.Message.MergeForward(ctx, req)
	if err != nil {
		return fmt.Errorf("merge forward: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("merge forward failed: %s", resp.Msg)
	}
	return nil
}
