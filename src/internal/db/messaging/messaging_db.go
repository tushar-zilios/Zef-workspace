package messaging

import (
	"context"
	"errors"
	"time"

	"workspace/src/internal/db"
	models "workspace/src/internal/models/messaging"

	"github.com/jackc/pgx/v5"
)

var ErrDBNotConfigured = errors.New("database not configured")

const messageColumns = `message_id, conversation_id, sender_id, sender_name, body, attachment_key, attachment_kind, attachment_name, attachment_size_bytes, created_at, updated_at, scheduled_for, status, view_once, forwarded_from_message_id, forwarded_from_sender_id, thread_root_message_id, shared_task_id, shared_task_title, shared_task_status, shared_task_number`

func scanMessage(row pgx.Row, m *models.Message) error {
	return row.Scan(&m.MessageID, &m.ConversationID, &m.SenderID, &m.SenderName, &m.Body, &m.AttachmentKey, &m.AttachmentKind, &m.AttachmentName, &m.AttachmentSizeBytes, &m.CreatedAt, &m.UpdatedAt, &m.ScheduledFor, &m.Status, &m.ViewOnce, &m.ForwardedFromMessageID, &m.ForwardedFromSenderID, &m.ThreadRootMessageID, &m.SharedTaskID, &m.SharedTaskTitle, &m.SharedTaskStatus, &m.SharedTaskNumber)
}

func attachMembersAndLastMessage(ctx context.Context, convs []models.Conversation) error {
	if len(convs) == 0 {
		return nil
	}
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}

	ids := make([]string, len(convs))
	idxByID := make(map[string]int, len(convs))
	for i := range convs {
		ids[i] = convs[i].ConversationID
		idxByID[convs[i].ConversationID] = i
	}

	memberRows, err := pool.Query(ctx, `
		SELECT conversation_id, user_id FROM public.conversation_members WHERE conversation_id = ANY($1)
	`, ids)
	if err != nil {
		return err
	}
	for memberRows.Next() {
		var convID, uid string
		if err := memberRows.Scan(&convID, &uid); err != nil {
			memberRows.Close()
			return err
		}
		if i, ok := idxByID[convID]; ok {
			convs[i].Members = append(convs[i].Members, uid)
		}
	}
	memberRows.Close()
	if err := memberRows.Err(); err != nil {
		return err
	}

	lastRows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (conversation_id) `+messageColumns+`
		FROM public.messages
		WHERE conversation_id = ANY($1) AND deleted_at IS NULL AND status = 'sent'
		ORDER BY conversation_id, created_at DESC
	`, ids)
	if err != nil {
		return err
	}
	defer lastRows.Close()
	for lastRows.Next() {
		var m models.Message
		if err := scanMessage(lastRows, &m); err != nil {
			return err
		}
		if i, ok := idxByID[m.ConversationID]; ok {
			mCopy := m
			convs[i].LastMessage = &mCopy
		}
	}
	return lastRows.Err()
}

func ListConversations(ctx context.Context, workspaceID, userID string) ([]models.Conversation, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	rows, err := pool.Query(ctx, `
		SELECT c.conversation_id, c.workspace_id, c.type, c.name, c.created_by, c.created_at, c.updated_at,
			(SELECT COUNT(*) FROM public.messages m
			 WHERE m.conversation_id = c.conversation_id AND m.deleted_at IS NULL AND m.status = 'sent'
			   AND m.sender_id != $2 AND m.created_at > cm.last_read_at)
		FROM public.conversations c
		JOIN public.conversation_members cm ON cm.conversation_id = c.conversation_id
		WHERE c.workspace_id = $1 AND cm.user_id = $2
		ORDER BY c.updated_at DESC
	`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	var out []models.Conversation
	for rows.Next() {
		var c models.Conversation
		if err := rows.Scan(&c.ConversationID, &c.WorkspaceID, &c.Type, &c.Name, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt, &c.UnreadCount); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := attachMembersAndLastMessage(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// MarkConversationRead bumps the calling member's last_read_at to now, zeroing their unread
// count for this conversation until a newer message arrives.
func MarkConversationRead(ctx context.Context, conversationID, userID string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	tag, err := pool.Exec(ctx, `
		UPDATE public.conversation_members SET last_read_at = NOW()
		WHERE conversation_id = $1 AND user_id = $2
	`, conversationID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func FindDirectConversation(ctx context.Context, workspaceID, userA, userB string) (*models.Conversation, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	var c models.Conversation
	err := pool.QueryRow(ctx, `
		SELECT c.conversation_id, c.workspace_id, c.type, c.name, c.created_by, c.created_at, c.updated_at
		FROM public.conversations c
		WHERE c.workspace_id = $1 AND c.type = 'direct'
		AND EXISTS (SELECT 1 FROM public.conversation_members WHERE conversation_id = c.conversation_id AND user_id = $2)
		AND EXISTS (SELECT 1 FROM public.conversation_members WHERE conversation_id = c.conversation_id AND user_id = $3)
		AND (SELECT COUNT(*) FROM public.conversation_members WHERE conversation_id = c.conversation_id) = 2
		LIMIT 1
	`, workspaceID, userA, userB).Scan(&c.ConversationID, &c.WorkspaceID, &c.Type, &c.Name, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func CreateConversation(ctx context.Context, workspaceID, convType string, name *string, createdBy string, memberIDs []string) (*models.Conversation, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var c models.Conversation
	err = tx.QueryRow(ctx, `
		INSERT INTO public.conversations (workspace_id, type, name, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING conversation_id, workspace_id, type, name, created_by, created_at, updated_at
	`, workspaceID, convType, name, createdBy).Scan(&c.ConversationID, &c.WorkspaceID, &c.Type, &c.Name, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}

	allMembers := append([]string{createdBy}, memberIDs...)
	seen := map[string]bool{}
	for _, uid := range allMembers {
		if uid == "" || seen[uid] {
			continue
		}
		seen[uid] = true
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.conversation_members (conversation_id, user_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, c.ConversationID, uid); err != nil {
			return nil, err
		}
		c.Members = append(c.Members, uid)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &c, nil
}

func GetConversation(ctx context.Context, conversationID string) (*models.Conversation, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	var c models.Conversation
	err := pool.QueryRow(ctx, `
		SELECT conversation_id, workspace_id, type, name, created_by, created_at, updated_at
		FROM public.conversations WHERE conversation_id = $1
	`, conversationID).Scan(&c.ConversationID, &c.WorkspaceID, &c.Type, &c.Name, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MessageInConversation reports whether messageID actually belongs to conversationID. Callers
// that scope an action by URL {id} (conversation) and {message_id} must call this after the
// membership check — membership in a conversation must never authorize acting on a message that
// lives in a different conversation.
func MessageInConversation(ctx context.Context, messageID, conversationID string) (bool, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return false, ErrDBNotConfigured
	}
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM public.messages WHERE message_id = $1 AND conversation_id = $2)
	`, messageID, conversationID).Scan(&exists)
	return exists, err
}

func IsMember(ctx context.Context, conversationID, userID string) (bool, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return false, ErrDBNotConfigured
	}
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM public.conversation_members WHERE conversation_id = $1 AND user_id = $2)
	`, conversationID, userID).Scan(&exists)
	return exists, err
}

func ListMemberIDs(ctx context.Context, conversationID string) ([]string, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	rows, err := pool.Query(ctx, `SELECT user_id FROM public.conversation_members WHERE conversation_id = $1`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

func ListMessages(ctx context.Context, conversationID string, before time.Time, limit int, requestingUserID string) ([]models.Message, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	var rows pgx.Rows
	var err error
	// Tie-break by message_id whenever created_at collides (bulk inserts, clock coarseness),
	// otherwise the created_at-only ORDER BY is nondeterministic across pages and a caller
	// paginating with `before` can skip or duplicate rows that share a timestamp.
	if before.IsZero() {
		rows, err = pool.Query(ctx, `
			SELECT `+messageColumns+`
			FROM public.messages WHERE conversation_id = $1 AND deleted_at IS NULL AND status = 'sent' AND thread_root_message_id IS NULL
			ORDER BY created_at DESC, message_id DESC LIMIT $2
		`, conversationID, limit)
	} else {
		rows, err = pool.Query(ctx, `
			SELECT `+messageColumns+`
			FROM public.messages WHERE conversation_id = $1 AND created_at < $2 AND deleted_at IS NULL AND status = 'sent' AND thread_root_message_id IS NULL
			ORDER BY created_at DESC, message_id DESC LIMIT $3
		`, conversationID, before, limit)
	}
	if err != nil {
		return nil, err
	}
	var out []models.Message
	for rows.Next() {
		var m models.Message
		if err := scanMessage(rows, &m); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := attachReactions(ctx, out, requestingUserID); err != nil {
		return nil, err
	}
	if err := attachViewOnceState(ctx, out, requestingUserID); err != nil {
		return nil, err
	}
	if err := attachReplyCounts(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

type SharedTaskRef struct {
	TaskID string
	Title  *string
	Status *string
	Number *int
}

func CreateMessage(ctx context.Context, conversationID, senderID, senderName, body string, attachmentKey, attachmentKind, attachmentName *string, attachmentSizeBytes *int64, scheduledFor *time.Time, viewOnce bool, forwardedFromMessageID, forwardedFromSenderID, threadRootMessageID *string, sharedTask *SharedTaskRef) (*models.Message, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	status := "sent"
	if scheduledFor != nil {
		status = "scheduled"
	}
	var sharedTaskID, sharedTaskTitle, sharedTaskStatus *string
	var sharedTaskNumber *int
	if sharedTask != nil {
		sharedTaskID = &sharedTask.TaskID
		sharedTaskTitle = sharedTask.Title
		sharedTaskStatus = sharedTask.Status
		sharedTaskNumber = sharedTask.Number
	}
	var m models.Message
	row := pool.QueryRow(ctx, `
		INSERT INTO public.messages (conversation_id, sender_id, sender_name, body, attachment_key, attachment_kind, attachment_name, attachment_size_bytes, scheduled_for, status, view_once, forwarded_from_message_id, forwarded_from_sender_id, thread_root_message_id, shared_task_id, shared_task_title, shared_task_status, shared_task_number)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING `+messageColumns+`
	`, conversationID, senderID, senderName, body, attachmentKey, attachmentKind, attachmentName, attachmentSizeBytes, scheduledFor, status, viewOnce, forwardedFromMessageID, forwardedFromSenderID, threadRootMessageID, sharedTaskID, sharedTaskTitle, sharedTaskStatus, sharedTaskNumber)
	if err := scanMessage(row, &m); err != nil {
		return nil, err
	}
	if status == "sent" {
		_, _ = pool.Exec(ctx, `UPDATE public.conversations SET updated_at = NOW() WHERE conversation_id = $1`, conversationID)
	}
	return &m, nil
}

var (
	ErrMessageNotEditable = errors.New("message not found, not owned by user, or past the edit window")
	ErrMessageNotOwned    = errors.New("message not found or not owned by user")
)

func UpdateMessage(ctx context.Context, messageID, senderID, newBody string) (*models.Message, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	var m models.Message
	row := pool.QueryRow(ctx, `
		UPDATE public.messages SET body = $1, updated_at = NOW()
		WHERE message_id = $2 AND sender_id = $3 AND deleted_at IS NULL
		  AND created_at > NOW() - INTERVAL '5 minutes'
		RETURNING `+messageColumns+`
	`, newBody, messageID, senderID)
	err := scanMessage(row, &m)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMessageNotEditable
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func DeleteMessage(ctx context.Context, messageID, senderID string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	tag, err := pool.Exec(ctx, `
		UPDATE public.messages SET deleted_at = NOW()
		WHERE message_id = $1 AND sender_id = $2 AND deleted_at IS NULL
	`, messageID, senderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMessageNotOwned
	}
	return nil
}

// ListPurgeableMessages returns soft-deleted messages whose deleted_at is older than olderThan,
// including their attachment_key so the caller can also remove the backing GCS object.
func ListPurgeableMessages(ctx context.Context, olderThan time.Time, limit int) ([]models.Message, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	rows, err := pool.Query(ctx, `
		SELECT `+messageColumns+`
		FROM public.messages
		WHERE deleted_at IS NOT NULL AND deleted_at < $1
		LIMIT $2
	`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Message
	for rows.Next() {
		var m models.Message
		if err := scanMessage(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// HardDeleteMessage permanently removes a soft-deleted message row (and cascades to its
// reactions/views via FK) after retention has been purged. Caller is responsible for deleting
// the backing attachment object first.
func HardDeleteMessage(ctx context.Context, messageID string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	_, err := pool.Exec(ctx, `DELETE FROM public.messages WHERE message_id = $1 AND deleted_at IS NOT NULL`, messageID)
	return err
}

func ListDueScheduledMessages(ctx context.Context) ([]models.Message, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	rows, err := pool.Query(ctx, `
		SELECT `+messageColumns+`
		FROM public.messages WHERE status = 'scheduled' AND scheduled_for <= NOW() AND deleted_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Message
	for rows.Next() {
		var m models.Message
		if err := scanMessage(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func MarkMessageSent(ctx context.Context, messageID string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	_, err := pool.Exec(ctx, `UPDATE public.messages SET status = 'sent' WHERE message_id = $1`, messageID)
	if err == nil {
		_, _ = pool.Exec(ctx, `
			UPDATE public.conversations SET updated_at = NOW()
			WHERE conversation_id = (SELECT conversation_id FROM public.messages WHERE message_id = $1)
		`, messageID)
	}
	return err
}

func ListScheduledMessages(ctx context.Context, conversationID, senderID string) ([]models.Message, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	rows, err := pool.Query(ctx, `
		SELECT `+messageColumns+`
		FROM public.messages
		WHERE conversation_id = $1 AND sender_id = $2 AND status = 'scheduled' AND deleted_at IS NULL
		ORDER BY scheduled_for ASC
	`, conversationID, senderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Message
	for rows.Next() {
		var m models.Message
		if err := scanMessage(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func attachReactions(ctx context.Context, messages []models.Message, requestingUserID string) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, len(messages))
	for i, m := range messages {
		ids[i] = m.MessageID
	}
	grouped, err := ListReactionsForMessages(ctx, ids, requestingUserID)
	if err != nil {
		return err
	}
	for i := range messages {
		messages[i].Reactions = grouped[messages[i].MessageID]
	}
	return nil
}

func AddReaction(ctx context.Context, messageID, userID, emoji string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO public.message_reactions (message_id, user_id, emoji)
		VALUES ($1, $2, $3)
		ON CONFLICT (message_id, user_id, emoji) DO NOTHING
	`, messageID, userID, emoji)
	return err
}

func RemoveReaction(ctx context.Context, messageID, userID, emoji string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	_, err := pool.Exec(ctx, `
		DELETE FROM public.message_reactions WHERE message_id = $1 AND user_id = $2 AND emoji = $3
	`, messageID, userID, emoji)
	return err
}

func ListReactionsForMessages(ctx context.Context, messageIDs []string, requestingUserID string) (map[string][]models.ReactionGroup, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	out := make(map[string][]models.ReactionGroup)
	if len(messageIDs) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT message_id, emoji, array_agg(user_id)
		FROM public.message_reactions
		WHERE message_id = ANY($1)
		GROUP BY message_id, emoji
	`, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var messageID, emoji string
		var userIDs []string
		if err := rows.Scan(&messageID, &emoji, &userIDs); err != nil {
			return nil, err
		}
		reacted := false
		for _, uid := range userIDs {
			if uid == requestingUserID {
				reacted = true
				break
			}
		}
		out[messageID] = append(out[messageID], models.ReactionGroup{
			Emoji:   emoji,
			Count:   len(userIDs),
			UserIDs: userIDs,
			Reacted: reacted,
		})
	}
	return out, rows.Err()
}

// attachViewOnceState mutates each view_once message in place: if requestingUserID already
// viewed it (and is not the sender), the attachment fields are cleared and Viewed=true is set.
// If requestingUserID is the sender, or the message isn't view_once, it's left untouched.
func attachViewOnceState(ctx context.Context, messages []models.Message, requestingUserID string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	var viewOnceIDs []string
	for _, m := range messages {
		if m.ViewOnce && m.SenderID != requestingUserID {
			viewOnceIDs = append(viewOnceIDs, m.MessageID)
		}
	}
	if len(viewOnceIDs) == 0 {
		return nil
	}
	rows, err := pool.Query(ctx, `
		SELECT message_id FROM public.message_views WHERE message_id = ANY($1) AND user_id = $2
	`, viewOnceIDs, requestingUserID)
	if err != nil {
		return err
	}
	defer rows.Close()
	viewed := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		viewed[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range messages {
		if messages[i].ViewOnce && messages[i].SenderID != requestingUserID && viewed[messages[i].MessageID] {
			messages[i].AttachmentKey = nil
			messages[i].AttachmentURL = nil
			messages[i].Viewed = true
		}
	}
	return nil
}

// MarkMessageViewed inserts (message_id, requestingUserID) into message_views if not already
// present and reports whether this call was the one that consumed the once-only reveal (true)
// vs. it was already consumed previously (false, meaning caller must NOT return the attachment).
func MarkMessageViewed(ctx context.Context, messageID, userID string) (bool, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return false, ErrDBNotConfigured
	}
	var returnedID string
	err := pool.QueryRow(ctx, `
		INSERT INTO public.message_views (message_id, user_id) VALUES ($1, $2)
		ON CONFLICT (message_id, user_id) DO NOTHING
		RETURNING message_id
	`, messageID, userID).Scan(&returnedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// attachReplyCounts batches a COUNT(*) ... GROUP BY thread_root_message_id query across the
// given message ids and sets ReplyCount on each, mirroring attachReactions's shape/signature.
func attachReplyCounts(ctx context.Context, messages []models.Message) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, len(messages))
	for i, m := range messages {
		ids[i] = m.MessageID
	}
	rows, err := pool.Query(ctx, `
		SELECT thread_root_message_id, COUNT(*)
		FROM public.messages
		WHERE thread_root_message_id = ANY($1) AND deleted_at IS NULL AND status = 'sent'
		GROUP BY thread_root_message_id
	`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var rootID string
		var count int
		if err := rows.Scan(&rootID, &count); err != nil {
			return err
		}
		counts[rootID] = count
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range messages {
		messages[i].ReplyCount = counts[messages[i].MessageID]
	}
	return nil
}

// ListThreadMessages returns the root message (message_id == rootMessageID) followed by all its
// replies ordered oldest-first, honoring the same deleted_at/status filters as ListMessages, and
// applying the same per-requesting-user view-once + reaction population.
func ListThreadMessages(ctx context.Context, rootMessageID, requestingUserID string) ([]models.Message, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	rows, err := pool.Query(ctx, `
		SELECT `+messageColumns+`
		FROM public.messages
		WHERE (message_id = $1 OR thread_root_message_id = $1) AND deleted_at IS NULL AND status = 'sent'
		ORDER BY created_at ASC
	`, rootMessageID)
	if err != nil {
		return nil, err
	}
	var out []models.Message
	for rows.Next() {
		var m models.Message
		if err := scanMessage(rows, &m); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := attachReactions(ctx, out, requestingUserID); err != nil {
		return nil, err
	}
	if err := attachViewOnceState(ctx, out, requestingUserID); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateReplyTarget checks that threadRootMessageID exists, belongs to conversationID, is not
// soft-deleted, and is not itself a reply (enforces the flat, non-nested thread model).
func ValidateReplyTarget(ctx context.Context, threadRootMessageID, conversationID string) error {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return ErrDBNotConfigured
	}
	var isReply bool
	err := pool.QueryRow(ctx, `
		SELECT thread_root_message_id IS NOT NULL
		FROM public.messages
		WHERE message_id = $1 AND conversation_id = $2 AND deleted_at IS NULL
	`, threadRootMessageID, conversationID).Scan(&isReply)
	if err != nil {
		return err
	}
	if isReply {
		return errors.New("cannot reply to a message that is itself a reply")
	}
	return nil
}

// ForwardMessage creates a new message (copying attachment_key/kind/name/size_bytes from the
// source, using the given caption as Body, tagging forwarded_from_*) in each of
// targetConversationIDs. Returns the created copies in the same order as targetConversationIDs.
// All memberships must already be verified by the caller (handler) before invoking this.
func ForwardMessage(ctx context.Context, sourceMessageID, forwarderID, forwarderName string, targetConversationIDs []string, caption string) ([]models.Message, error) {
	pool := db.GetPoolOrNil()
	if pool == nil {
		return nil, ErrDBNotConfigured
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var source models.Message
	row := tx.QueryRow(ctx, `
		SELECT `+messageColumns+`
		FROM public.messages WHERE message_id = $1 AND deleted_at IS NULL
	`, sourceMessageID)
	if err := scanMessage(row, &source); err != nil {
		return nil, err
	}

	out := make([]models.Message, 0, len(targetConversationIDs))
	for _, targetConversationID := range targetConversationIDs {
		var m models.Message
		copyRow := tx.QueryRow(ctx, `
			INSERT INTO public.messages (conversation_id, sender_id, sender_name, body, attachment_key, attachment_kind, attachment_name, attachment_size_bytes, status, forwarded_from_message_id, forwarded_from_sender_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'sent', $9, $10)
			RETURNING `+messageColumns+`
		`, targetConversationID, forwarderID, forwarderName, caption, source.AttachmentKey, source.AttachmentKind, source.AttachmentName, source.AttachmentSizeBytes, sourceMessageID, source.SenderID)
		if err := scanMessage(copyRow, &m); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE public.conversations SET updated_at = NOW() WHERE conversation_id = $1`, targetConversationID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
