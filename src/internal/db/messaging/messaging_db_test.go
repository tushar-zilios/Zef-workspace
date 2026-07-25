package messaging

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"workspace/src/internal/db"
	workspacedb "workspace/src/internal/db/workspace"

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

func seedWorkspace(t *testing.T, ownerID string) string {
	t.Helper()
	ws, err := workspacedb.CreateWorkspace(context.Background(), "msg-test-workspace", ownerID)
	if err != nil {
		t.Fatalf("seed CreateWorkspace failed: %v", err)
	}
	return ws.WorkspaceID
}

func TestCreateAndGetConversation_Direct(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)

	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	if conv.ConversationID == "" {
		t.Fatal("expected non-empty conversation ID")
	}

	got, err := GetConversation(ctx, conv.ConversationID)
	if err != nil {
		t.Fatalf("GetConversation failed: %v", err)
	}
	if got.Type != "direct" || got.WorkspaceID != workspaceID {
		t.Fatalf("unexpected conversation: %+v", got)
	}

	isMember, err := IsMember(ctx, conv.ConversationID, userA)
	if err != nil {
		t.Fatalf("IsMember failed: %v", err)
	}
	if !isMember {
		t.Fatal("expected creator to be a member")
	}

	isMemberB, err := IsMember(ctx, conv.ConversationID, userB)
	if err != nil {
		t.Fatalf("IsMember failed: %v", err)
	}
	if !isMemberB {
		t.Fatal("expected invited user to be a member")
	}
}

func TestFindDirectConversation(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)

	created, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}

	found, err := FindDirectConversation(ctx, workspaceID, userA, userB)
	if err != nil {
		t.Fatalf("FindDirectConversation failed: %v", err)
	}
	if found.ConversationID != created.ConversationID {
		t.Fatalf("expected to find created conversation, got %+v", found)
	}
}

func TestListConversations_ByWorkspaceAndMember(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)

	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}

	list, err := ListConversations(ctx, workspaceID, userA)
	if err != nil {
		t.Fatalf("ListConversations failed: %v", err)
	}
	if len(list) != 1 || list[0].ConversationID != conv.ConversationID {
		t.Fatalf("expected exactly the created conversation, got %+v", list)
	}
}

func TestCreateSendUpdateDeleteMessage(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)
	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}

	msg, err := CreateMessage(ctx, conv.ConversationID, userA, "Test User", "hello world", nil, nil, nil, nil, nil, false, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	if msg.MessageID == "" || msg.Status != "sent" {
		t.Fatalf("unexpected message after create: %+v", msg)
	}

	msgs, err := ListMessages(ctx, conv.ConversationID, time.Time{}, 50, userA)
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].MessageID != msg.MessageID {
		t.Fatalf("expected exactly the sent message, got %+v", msgs)
	}

	updated, err := UpdateMessage(ctx, msg.MessageID, userA, "hello updated")
	if err != nil {
		t.Fatalf("UpdateMessage failed: %v", err)
	}
	if updated.Body != "hello updated" {
		t.Fatalf("update did not persist: %+v", updated)
	}

	// Update by a non-owner should fail.
	if _, err := UpdateMessage(ctx, msg.MessageID, userB, "hijacked"); err == nil {
		t.Fatal("expected error updating message as non-owner")
	}

	if err := DeleteMessage(ctx, msg.MessageID, userA); err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}
	msgsAfterDelete, err := ListMessages(ctx, conv.ConversationID, time.Time{}, 50, userA)
	if err != nil {
		t.Fatalf("ListMessages after delete failed: %v", err)
	}
	if len(msgsAfterDelete) != 0 {
		t.Fatalf("expected soft-deleted message to be excluded, got %+v", msgsAfterDelete)
	}
}

func TestDeleteMessage_NotOwned(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)
	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	msg, err := CreateMessage(ctx, conv.ConversationID, userA, "Test User", "hello", nil, nil, nil, nil, nil, false, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}

	if err := DeleteMessage(ctx, msg.MessageID, userB); err == nil {
		t.Fatal("expected error deleting message as non-owner")
	}
}

func TestAddAndRemoveReaction(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)
	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	msg, err := CreateMessage(ctx, conv.ConversationID, userA, "Test User", "react to me", nil, nil, nil, nil, nil, false, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}

	if err := AddReaction(ctx, msg.MessageID, userB, "👍"); err != nil {
		t.Fatalf("AddReaction failed: %v", err)
	}

	grouped, err := ListReactionsForMessages(ctx, []string{msg.MessageID}, userB)
	if err != nil {
		t.Fatalf("ListReactionsForMessages failed: %v", err)
	}
	groups := grouped[msg.MessageID]
	if len(groups) != 1 || groups[0].Emoji != "👍" || groups[0].Count != 1 || !groups[0].Reacted {
		t.Fatalf("unexpected reaction groups: %+v", groups)
	}

	if err := RemoveReaction(ctx, msg.MessageID, userB, "👍"); err != nil {
		t.Fatalf("RemoveReaction failed: %v", err)
	}
	grouped, err = ListReactionsForMessages(ctx, []string{msg.MessageID}, userB)
	if err != nil {
		t.Fatalf("ListReactionsForMessages failed: %v", err)
	}
	if len(grouped[msg.MessageID]) != 0 {
		t.Fatalf("expected no reactions after removal, got %+v", grouped[msg.MessageID])
	}
}

func TestMarkMessageViewed_OnlyFirstCallReveals(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)
	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	key := "some-key"
	msg, err := CreateMessage(ctx, conv.ConversationID, userA, "Test User", "", &key, nil, nil, nil, nil, true, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}

	firstReveal, err := MarkMessageViewed(ctx, msg.MessageID, userB)
	if err != nil {
		t.Fatalf("MarkMessageViewed (first) failed: %v", err)
	}
	if !firstReveal {
		t.Fatal("expected first call to reveal")
	}

	secondReveal, err := MarkMessageViewed(ctx, msg.MessageID, userB)
	if err != nil {
		t.Fatalf("MarkMessageViewed (second) failed: %v", err)
	}
	if secondReveal {
		t.Fatal("expected second call to not re-reveal")
	}
}

func TestForwardMessage(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	userC := uniqueKey("user-c-")
	workspaceID := seedWorkspace(t, userA)
	sourceConv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation (source) failed: %v", err)
	}
	targetConv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userC})
	if err != nil {
		t.Fatalf("CreateConversation (target) failed: %v", err)
	}
	msg, err := CreateMessage(ctx, sourceConv.ConversationID, userA, "Test User", "forward me", nil, nil, nil, nil, nil, false, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}

	copies, err := ForwardMessage(ctx, msg.MessageID, userA, "Test User", []string{targetConv.ConversationID}, "fwd caption")
	if err != nil {
		t.Fatalf("ForwardMessage failed: %v", err)
	}
	if len(copies) != 1 {
		t.Fatalf("expected exactly one copy, got %+v", copies)
	}
	if copies[0].ForwardedFromMessageID == nil || *copies[0].ForwardedFromMessageID != msg.MessageID {
		t.Fatalf("expected forwarded_from_message_id to point to source, got %+v", copies[0])
	}
	if copies[0].Body != "fwd caption" {
		t.Fatalf("expected caption as body, got %+v", copies[0])
	}
}

func TestThreadReplies(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)
	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	root, err := CreateMessage(ctx, conv.ConversationID, userA, "Test User", "root message", nil, nil, nil, nil, nil, false, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateMessage (root) failed: %v", err)
	}

	if err := ValidateReplyTarget(ctx, root.MessageID, conv.ConversationID); err != nil {
		t.Fatalf("ValidateReplyTarget failed: %v", err)
	}

	reply, err := CreateMessage(ctx, conv.ConversationID, userB, "Test User", "a reply", nil, nil, nil, nil, nil, false, nil, nil, &root.MessageID, nil)
	if err != nil {
		t.Fatalf("CreateMessage (reply) failed: %v", err)
	}

	thread, err := ListThreadMessages(ctx, root.MessageID, userA)
	if err != nil {
		t.Fatalf("ListThreadMessages failed: %v", err)
	}
	if len(thread) != 2 {
		t.Fatalf("expected root + reply in thread, got %+v", thread)
	}
	if thread[0].MessageID != root.MessageID || thread[1].MessageID != reply.MessageID {
		t.Fatalf("expected thread ordered root then reply, got %+v", thread)
	}

	// A reply cannot itself be a reply target.
	if err := ValidateReplyTarget(ctx, reply.MessageID, conv.ConversationID); err == nil {
		t.Fatal("expected error validating a reply as a reply target")
	}
}

func TestListScheduledMessages_AndMarkSent(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)
	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}
	future := time.Now().Add(1 * time.Hour)
	msg, err := CreateMessage(ctx, conv.ConversationID, userA, "Test User", "later", nil, nil, nil, nil, &future, false, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	if msg.Status != "scheduled" {
		t.Fatalf("expected scheduled status, got %+v", msg)
	}

	scheduled, err := ListScheduledMessages(ctx, conv.ConversationID, userA)
	if err != nil {
		t.Fatalf("ListScheduledMessages failed: %v", err)
	}
	if len(scheduled) != 1 || scheduled[0].MessageID != msg.MessageID {
		t.Fatalf("expected exactly the scheduled message, got %+v", scheduled)
	}

	if err := MarkMessageSent(ctx, msg.MessageID); err != nil {
		t.Fatalf("MarkMessageSent failed: %v", err)
	}
	sentMsgs, err := ListMessages(ctx, conv.ConversationID, time.Time{}, 50, userA)
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	found := false
	for _, m := range sentMsgs {
		if m.MessageID == msg.MessageID && m.Status == "sent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected message to be marked sent, got %+v", sentMsgs)
	}
}

func TestListMemberIDs(t *testing.T) {
	requirePool(t)
	ctx := context.Background()
	userA := uniqueKey("user-a-")
	userB := uniqueKey("user-b-")
	workspaceID := seedWorkspace(t, userA)
	conv, err := CreateConversation(ctx, workspaceID, "direct", nil, userA, []string{userB})
	if err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}

	members, err := ListMemberIDs(ctx, conv.ConversationID)
	if err != nil {
		t.Fatalf("ListMemberIDs failed: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %+v", members)
	}
}
