package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	messagingdb "workspace/src/internal/db/messaging"

	"github.com/go-chi/chi/v5"
)

func TestAddAndRemoveReactionHandler(t *testing.T) {
	requirePool(t)
	_, userA, userB, conversationID := seedConversation(t)
	msg, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "react target", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages/{message_id}/reactions", AddReactionHandler)
	r.Delete("/messaging/conversations/{id}/messages/{message_id}/reactions", RemoveReactionHandler)

	addBody, _ := json.Marshal(reactionRequest{Emoji: "🔥"})
	addReq := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID+"/reactions", bytes.NewReader(addBody)), userB)
	addW := httptest.NewRecorder()
	r.ServeHTTP(addW, addReq)
	if addW.Code != http.StatusOK {
		t.Fatalf("expected 200 adding reaction, got %d: %s", addW.Code, addW.Body.String())
	}
	var addResp map[string]interface{}
	if err := json.NewDecoder(addW.Body).Decode(&addResp); err != nil {
		t.Fatalf("failed to decode add response: %v", err)
	}
	reactions, ok := addResp["reactions"].([]interface{})
	if !ok || len(reactions) != 1 {
		t.Fatalf("expected exactly one reaction group, got %+v", addResp)
	}

	removeBody, _ := json.Marshal(reactionRequest{Emoji: "🔥"})
	removeReq := withUser(httptest.NewRequest(http.MethodDelete, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID+"/reactions", bytes.NewReader(removeBody)), userB)
	removeW := httptest.NewRecorder()
	r.ServeHTTP(removeW, removeReq)
	if removeW.Code != http.StatusOK {
		t.Fatalf("expected 200 removing reaction, got %d: %s", removeW.Code, removeW.Body.String())
	}
	var removeResp map[string]interface{}
	if err := json.NewDecoder(removeW.Body).Decode(&removeResp); err != nil {
		t.Fatalf("failed to decode remove response: %v", err)
	}
	// A nil "reactions" (JSON null, from an empty/nil Go slice) or an empty array both mean
	// no reactions remain.
	if reactionsAfter, ok := removeResp["reactions"].([]interface{}); ok && len(reactionsAfter) != 0 {
		t.Fatalf("expected no reactions after removal, got %+v", removeResp)
	} else if !ok && removeResp["reactions"] != nil {
		t.Fatalf("expected nil or empty reactions after removal, got %+v", removeResp)
	}
}

func TestAddReactionHandler_MissingEmoji(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)
	msg, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "target", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages/{message_id}/reactions", AddReactionHandler)

	body, _ := json.Marshal(reactionRequest{})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID+"/reactions", bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAddReactionHandler_MessageFromOtherConversation is a regression test for the IDOR where
// membership of conversation A was enough to react to a message that actually lives in
// conversation B (as long as the messageID was guessed/known).
func TestAddReactionHandler_MessageFromOtherConversation(t *testing.T) {
	requirePool(t)
	workspaceID, userA, _, otherConversationID := seedConversation(t)
	otherMsg, err := messagingdb.CreateMessage(context.Background(), otherConversationID, userA, "Test User", "lives in other conv", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}
	// userA is also a member of this second conversation (same workspace), but otherMsg
	// belongs to otherConversationID, not myConv.
	secondConvName := "second conv"
	myConv, err := messagingdb.CreateConversation(context.Background(), workspaceID, "group", &secondConvName, userA, nil)
	if err != nil {
		t.Fatalf("seed second CreateConversation failed: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages/{message_id}/reactions", AddReactionHandler)

	body, _ := json.Marshal(reactionRequest{Emoji: "👀"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+myConv.ConversationID+"/messages/"+otherMsg.MessageID+"/reactions", bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-conversation message id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAddReactionHandler_NotMember(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)
	msg, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "target", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}
	outsider := uniqueKey("outsider-")

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages/{message_id}/reactions", AddReactionHandler)

	body, _ := json.Marshal(reactionRequest{Emoji: "👀"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID+"/reactions", bytes.NewReader(body)), outsider)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
