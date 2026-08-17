package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"workspace/src/internal/db"
	messagingdb "workspace/src/internal/db/messaging"
	workspacedb "workspace/src/internal/db/workspace"
	messagingModels "workspace/src/internal/models/messaging"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func TestMain(m *testing.M) {
	_ = godotenv.Load("../../../../.env")
	if os.Getenv("RUN_DB_TESTS") != "1" {
		os.Exit(0)
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		os.Exit(0)
	}
	if _, err := db.InitDB(context.Background(), dbURL, "development"); err != nil {
		os.Exit(0)
	}
	code := m.Run()
	db.CloseDB()
	os.Exit(code)
}

func requirePool(t *testing.T) {
	t.Helper()
	if db.GetPoolOrNil() == nil {
		t.Skip("DATABASE_URL not set; skipping DB test")
	}
}

func uniqueKey(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

// withUser returns req with the given user_id stashed in context, matching how
// JWTMiddleware populates it (see routes/middleware.go: context.WithValue(ctx, "user_id", ...)).
func withUser(req *http.Request, uid string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), "user_id", uid))
}

func seedConversation(t *testing.T) (workspaceID, userA, userB, conversationID string) {
	t.Helper()
	userA = uniqueKey("user-a-")
	userB = uniqueKey("user-b-")
	ws, err := workspacedb.CreateWorkspace(context.Background(), "msg-handler-test", userA)
	if err != nil {
		t.Fatalf("seed CreateWorkspace failed: %v", err)
	}
	conv, err := messagingdb.CreateConversation(context.Background(), ws.WorkspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("seed CreateConversation failed: %v", err)
	}
	return ws.WorkspaceID, userA, userB, conv.ConversationID
}

func TestListConversationsHandler_MissingWorkspaceID(t *testing.T) {
	requirePool(t)
	req := withUser(httptest.NewRequest(http.MethodGet, "/messaging/conversations", nil), uniqueKey("user-"))
	w := httptest.NewRecorder()

	ListConversationsHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListConversationsHandler_Success(t *testing.T) {
	requirePool(t)
	workspaceID, userA, _, _ := seedConversation(t)

	req := withUser(httptest.NewRequest(http.MethodGet, "/messaging/conversations?workspace_id="+workspaceID, nil), userA)
	w := httptest.NewRecorder()

	ListConversationsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var convs []messagingModels.Conversation
	if err := json.NewDecoder(w.Body).Decode(&convs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected exactly one conversation, got %+v", convs)
	}
}

func TestCreateConversationHandler_ValidationErrors(t *testing.T) {
	requirePool(t)
	uid := uniqueKey("user-")

	cases := []struct {
		name string
		body createConversationRequest
	}{
		{"missing workspace_id", createConversationRequest{Type: "direct", MemberIDs: []string{"x"}}},
		{"direct without exactly one member", createConversationRequest{WorkspaceID: uuid.New().String(), Type: "direct", MemberIDs: []string{}}},
		{"group without name", createConversationRequest{WorkspaceID: uuid.New().String(), Type: "group", MemberIDs: []string{"x"}}},
		{"invalid type", createConversationRequest{WorkspaceID: uuid.New().String(), Type: "bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations", bytes.NewReader(body)), uid)
			w := httptest.NewRecorder()

			CreateConversationHandler(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCreateConversationHandler_DirectSuccessAndIdempotent(t *testing.T) {
	requirePool(t)
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := uuid.New().String()

	body, _ := json.Marshal(createConversationRequest{WorkspaceID: workspaceID, Type: "direct", MemberIDs: []string{userB}})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations", bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	CreateConversationHandler(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var conv messagingModels.Conversation
	if err := json.NewDecoder(w.Body).Decode(&conv); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Second call for the same pair should return the existing conversation with 200.
	req2 := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations", bytes.NewReader(body)), userA)
	w2 := httptest.NewRecorder()
	CreateConversationHandler(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 on repeat direct conversation creation, got %d: %s", w2.Code, w2.Body.String())
	}
	var conv2 messagingModels.Conversation
	if err := json.NewDecoder(w2.Body).Decode(&conv2); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if conv2.ConversationID != conv.ConversationID {
		t.Fatalf("expected same conversation returned, got %+v vs %+v", conv, conv2)
	}
}

func TestListMessagesHandler_NotMember(t *testing.T) {
	requirePool(t)
	_, _, _, conversationID := seedConversation(t)
	outsider := uniqueKey("outsider-")

	r := chi.NewRouter()
	r.Get("/messaging/conversations/{id}/messages", ListMessagesHandler)

	req := withUser(httptest.NewRequest(http.MethodGet, "/messaging/conversations/"+conversationID+"/messages", nil), outsider)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSendAndListMessagesHandler(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages", SendMessageHandler)
	r.Get("/messaging/conversations/{id}/messages", ListMessagesHandler)

	sendBody, _ := json.Marshal(sendMessageRequest{SenderName: "Test User", Body: "hello handler"})
	sendReq := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages", bytes.NewReader(sendBody)), userA)
	sendW := httptest.NewRecorder()
	r.ServeHTTP(sendW, sendReq)
	if sendW.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", sendW.Code, sendW.Body.String())
	}

	listReq := withUser(httptest.NewRequest(http.MethodGet, "/messaging/conversations/"+conversationID+"/messages", nil), userA)
	listW := httptest.NewRecorder()
	r.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listW.Code, listW.Body.String())
	}
	var msgs []messagingModels.Message
	if err := json.NewDecoder(listW.Body).Decode(&msgs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Body != "hello handler" {
		t.Fatalf("expected exactly the sent message, got %+v", msgs)
	}
}

func TestSendMessageHandler_EmptyBodyRejected(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages", SendMessageHandler)

	body, _ := json.Marshal(sendMessageRequest{})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages", bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSendMessageHandler_BodyTooLongRejected(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)

	r := chi.NewRouter()
	r.Post("/messaging/conversations/{id}/messages", SendMessageHandler)

	overLong := make([]byte, maxMessageBodyRunes+1)
	for i := range overLong {
		overLong[i] = 'a'
	}
	body, _ := json.Marshal(sendMessageRequest{SenderName: "Test User", Body: string(overLong)})
	req := withUser(httptest.NewRequest(http.MethodPost, "/messaging/conversations/"+conversationID+"/messages", bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for over-length body, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateMessageHandler_MessageFromOtherConversation is a regression test for the IDOR where
// membership of conversation A was enough to edit a message that actually lives in conversation B.
func TestUpdateMessageHandler_MessageFromOtherConversation(t *testing.T) {
	requirePool(t)
	workspaceID, userA, _, otherConversationID := seedConversation(t)
	otherMsg, err := messagingdb.CreateMessage(context.Background(), otherConversationID, userA, "Test User", "lives in other conv", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}
	secondConvName := "second conv"
	myConv, err := messagingdb.CreateConversation(context.Background(), workspaceID, "group", &secondConvName, userA, nil)
	if err != nil {
		t.Fatalf("seed second CreateConversation failed: %v", err)
	}

	r := chi.NewRouter()
	r.Put("/messaging/conversations/{id}/messages/{message_id}", UpdateMessageHandler)

	body, _ := json.Marshal(updateMessageRequest{Body: "edited"})
	req := withUser(httptest.NewRequest(http.MethodPut, "/messaging/conversations/"+myConv.ConversationID+"/messages/"+otherMsg.MessageID, bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-conversation message id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateMessageHandler(t *testing.T) {
	requirePool(t)
	_, userA, userB, conversationID := seedConversation(t)
	msg, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "original", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}

	r := chi.NewRouter()
	r.Put("/messaging/conversations/{id}/messages/{message_id}", UpdateMessageHandler)

	body, _ := json.Marshal(updateMessageRequest{Body: "edited"})
	req := withUser(httptest.NewRequest(http.MethodPut, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID, bytes.NewReader(body)), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got messagingModels.Message
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Body != "edited" {
		t.Fatalf("unexpected message: %+v", got)
	}

	// Non-owner edit should be forbidden.
	req2 := withUser(httptest.NewRequest(http.MethodPut, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID, bytes.NewReader(body)), userB)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner edit, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestDeleteMessageHandler(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)
	msg, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "to be deleted", nil, nil, nil, nil, nil, false, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}

	r := chi.NewRouter()
	r.Delete("/messaging/conversations/{id}/messages/{message_id}", DeleteMessageHandler)

	req := withUser(httptest.NewRequest(http.MethodDelete, "/messaging/conversations/"+conversationID+"/messages/"+msg.MessageID, nil), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListScheduledMessagesHandler(t *testing.T) {
	requirePool(t)
	_, userA, _, conversationID := seedConversation(t)
	future := time.Now().Add(1 * time.Hour)
	if _, err := messagingdb.CreateMessage(context.Background(), conversationID, userA, "Test User", "later", nil, nil, nil, nil, &future, false, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed CreateMessage failed: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/messaging/conversations/{id}/scheduled", ListScheduledMessagesHandler)

	req := withUser(httptest.NewRequest(http.MethodGet, "/messaging/conversations/"+conversationID+"/scheduled", nil), userA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var msgs []messagingModels.Message
	if err := json.NewDecoder(w.Body).Decode(&msgs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly one scheduled message, got %+v", msgs)
	}
}
