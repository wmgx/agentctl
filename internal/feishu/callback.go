package feishu

import (
	"encoding/json"
	"log"
	"sync"
)

type PendingAction struct {
	mu      sync.Mutex
	pending map[string]chan ActionResult
}

type ActionResult struct {
	Action    string
	Value     map[string]string
	FormValue map[string]string
}

func NewPendingAction() *PendingAction {
	return &PendingAction{
		pending: make(map[string]chan ActionResult),
	}
}

func (pa *PendingAction) Wait(requestID string) chan ActionResult {
	ch := make(chan ActionResult, 1)
	pa.mu.Lock()
	pa.pending[requestID] = ch
	pa.mu.Unlock()
	return ch
}

func (pa *PendingAction) Resolve(requestID string, result ActionResult) bool {
	pa.mu.Lock()
	ch, ok := pa.pending[requestID]
	if ok {
		delete(pa.pending, requestID)
	}
	pa.mu.Unlock()

	if !ok {
		log.Printf("[pending] Resolve: no channel for request_id=%s (stale card?)", requestID)
		return false
	}
	ch <- result
	close(ch)
	log.Printf("[pending] Resolve: dispatched action=%s for request_id=%s", result.Action, requestID)
	return true
}

func (pa *PendingAction) ResolveAll(result ActionResult) {
	pa.mu.Lock()
	for id, ch := range pa.pending {
		ch <- result
		close(ch)
		delete(pa.pending, id)
	}
	pa.mu.Unlock()
}

func ParseCardCallback(body []byte) (*CardAction, error) {
	var cb struct {
		OpenID string `json:"open_id"`
		Action struct {
			Value json.RawMessage `json:"value"`
		} `json:"action"`
	}
	if err := json.Unmarshal(body, &cb); err != nil {
		return nil, err
	}

	var value map[string]string
	if err := json.Unmarshal(cb.Action.Value, &value); err != nil {
		return nil, err
	}

	return &CardAction{
		OpenID: cb.OpenID,
		Action: value["action"],
		Value:  value,
	}, nil
}
