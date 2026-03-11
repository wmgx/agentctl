package feishu

import (
	"context"
	"encoding/json"
	"log"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type IncomingMessage struct {
	ChatID          string
	MessageID       string
	ParentMessageID string // 引用的父消息 ID，私聊引用时非空
	SenderID        string
	Text            string
	MsgType         string
	ChatType        string
}

type MessageHandler func(ctx context.Context, msg IncomingMessage)
type CardActionHandler func(ctx context.Context, action CardAction) string
type ChatDisbandHandler func(ctx context.Context, chatID string)

type CardAction struct {
	OpenID    string
	ChatID    string
	MessageID string
	Action    string
	Value     map[string]string
	FormValue map[string]string
}

type EventListener struct {
	appID         string
	appSecret     string
	onMessage     MessageHandler
	onCardAction  CardActionHandler
	onChatDisband ChatDisbandHandler
}

func NewEventListener(appID, appSecret string) *EventListener {
	return &EventListener{
		appID:     appID,
		appSecret: appSecret,
	}
}

func (el *EventListener) OnMessage(handler MessageHandler) {
	el.onMessage = handler
}

func (el *EventListener) OnCardAction(handler CardActionHandler) {
	el.onCardAction = handler
}

func (el *EventListener) OnChatDisband(handler ChatDisbandHandler) {
	el.onChatDisband = handler
}

func (el *EventListener) Start(ctx context.Context) error {
	eventDispatcher := dispatcher.NewEventDispatcher("", "")

	eventDispatcher.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		if el.onMessage == nil {
			return nil
		}
		msg := event.Event.Message
		sender := event.Event.Sender

		log.Printf("[event] message received: chat_type=%s, chat_id=%s, msg_type=%s, sender=%s",
			*msg.ChatType, *msg.ChatId, *msg.MessageType, *sender.SenderId.OpenId)

		incoming := IncomingMessage{
			ChatID:    *msg.ChatId,
			MessageID: *msg.MessageId,
			SenderID:  *sender.SenderId.OpenId,
			MsgType:   *msg.MessageType,
			ChatType:  *msg.ChatType,
		}
		if msg.ParentId != nil {
			incoming.ParentMessageID = *msg.ParentId
		}

		if *msg.MessageType == "text" {
			incoming.Text = extractText(*msg.Content)
			log.Printf("[event] text content: %s", incoming.Text)
		}

		el.onMessage(ctx, incoming)
		return nil
	})

	eventDispatcher.OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		if el.onCardAction == nil || event.Event == nil {
			return nil, nil
		}
		req := event.Event

		var openID, msgID, chatID string
		if req.Operator != nil {
			openID = req.Operator.OpenID
		}
		if req.Context != nil {
			msgID = req.Context.OpenMessageID
			chatID = req.Context.OpenChatID
		}

		strValue := make(map[string]string)
		formValue := make(map[string]string)
		if req.Action != nil {
			for k, v := range req.Action.Value {
				if s, ok := v.(string); ok {
					strValue[k] = s
				}
			}
			for k, v := range req.Action.FormValue {
				if s, ok := v.(string); ok {
					formValue[k] = s
				}
			}
		}

		action := CardAction{
			OpenID:    openID,
			ChatID:    chatID,
			MessageID: msgID,
			Action:    strValue["action"],
			Value:     strValue,
			FormValue: formValue,
		}
		log.Printf("[event] card action: open_id=%s, action=%s, request_id=%s", openID, action.Action, strValue["request_id"])
		resp := el.onCardAction(ctx, action)
		if resp != "" {
			var cardResp map[string]interface{}
			if err := json.Unmarshal([]byte(resp), &cardResp); err == nil {
				return &callback.CardActionTriggerResponse{
					Card: &callback.Card{
						Type: "card_json",
						Data: cardResp["card"],
					},
				}, nil
			}
		}
		return nil, nil
	})

	eventDispatcher.OnP2ChatDisbandedV1(func(ctx context.Context, event *larkim.P2ChatDisbandedV1) error {
		if el.onChatDisband == nil || event.Event == nil || event.Event.ChatId == nil {
			return nil
		}
		chatID := *event.Event.ChatId
		log.Printf("[event] chat disbanded: chat_id=%s", chatID)
		el.onChatDisband(ctx, chatID)
		return nil
	})

	// 注册 reaction 事件的空处理器，避免 SDK 输出 "[Error] not found handler" 噪音日志
	// 这些事件由 bot 自身添加/移除 OnIt emoji 产生，无需业务处理
	eventDispatcher.OnP2MessageReactionCreatedV1(func(_ context.Context, _ *larkim.P2MessageReactionCreatedV1) error {
		return nil
	})
	eventDispatcher.OnP2MessageReactionDeletedV1(func(_ context.Context, _ *larkim.P2MessageReactionDeletedV1) error {
		return nil
	})

	wsClient := larkws.NewClient(el.appID, el.appSecret,
		larkws.WithEventHandler(eventDispatcher),
		larkws.WithLogLevel(larkcore.LogLevelDebug),
	)

	log.Println("[event] starting WebSocket client...")
	return wsClient.Start(ctx)
}

func extractText(content string) string {
	var c struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &c); err != nil {
		log.Printf("extractText error: %v, content: %s", err, content)
		return content
	}
	return c.Text
}
