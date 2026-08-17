package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	messagingdb "workspace/src/internal/db/messaging"
	workspacedb "workspace/src/internal/db/workspace"
	messagingModels "workspace/src/internal/models/messaging"

	"github.com/go-chi/chi/v5"
)

func TestForwardMessageHandler_MissingTargets(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)
	msg, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "to forward", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages/{message_id}/forward", ForwardMessageHandler)

	body, _ := json.Marshal(forwardMessageRequest{})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID+"/forward", bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardMessageHandler_NotMemberOfTarget(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)
	msg, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "to forward", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}
	// A conversation userA is not a member of.
	otherOwner := uniqueKey("other-owner-")
	otherWs, err := workspacedb.CreateWorkspace(context.Background(), "other-ws", otherOwner)
	if err != nil {
		t.Fatalf("seed CreateWorkspace failed: %v", err)
	}
	otherConv, err := messagingdb.CreateConversation(context.Background(), otherWs.WorkspaceID, "direct", nil, otherOwner, []string{uniqueKey("user-")})
	if err != nil {
		t.Fatalf("seed CreateConversation failed: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages/{message_id}/forward", ForwardMessageHandler)

	body, _ := json.Marshal(forwardMessageRequest{TargetConversationIDs: []string{otherConv.ConversationID}, ForwarderName: "Test User"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID+"/forward", bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestForwardMessageHandler_Success(t *testing.T) {
	requirePool(t)
	workspaceID, userA, userC, conversationID := seedConversation(t)
	msg, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "to forward", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}
	targetConv, err := messagingdb.CreateConversation(context.Background(), workspaceID, "direct", nil, userA, []string{userC})
	if err != nil {
		t.Fatalf("seed CreateConversation (target) failed: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages/{message_id}/forward", ForwardMessageHandler)

	body, _ := json.Marshal(forwardMessageRequest{TargetConversationIDs: []string{targetConv.ConversationID}, Caption: "fwd", ForwarderName: "Test User"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID+"/forward", bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var copies []messagingModels.Message
	if err := json.NewDecoder(w.Body).Decode(&copies); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(copies) != 1 || copies[0].Body != "fwd" {
		t.Fatalf("unexpected forwarded copies: %+v", copies)
	}
}
