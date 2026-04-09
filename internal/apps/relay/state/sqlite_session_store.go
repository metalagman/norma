package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type sqliteSessionStore struct {
	db *sql.DB
}

func (s *sqliteSessionStore) Upsert(ctx context.Context, record SessionRecord) error {
	sessionID := strings.TrimSpace(record.SessionID)
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	channelType := strings.TrimSpace(record.ChannelType)
	if channelType == "" {
		return fmt.Errorf("channel_type is required")
	}
	addressKey := strings.TrimSpace(record.AddressKey)
	if addressKey == "" {
		return fmt.Errorf("address_key is required")
	}
	addressJSON := strings.TrimSpace(record.AddressJSON)
	if addressJSON == "" {
		return fmt.Errorf("address_json is required")
	}

	if strings.TrimSpace(record.Status) == "" {
		record.Status = SessionStatusActive
	}

	chatID, topicID, err := telegramTuple(channelType, addressJSON)
	if err != nil {
		return fmt.Errorf("decode telegram tuple: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO relay_session_metadata (
			session_id, chat_id, topic_id, channel_type, address_key, address_json, agent_name, workspace_dir, branch_name, status, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			chat_id = excluded.chat_id,
			topic_id = excluded.topic_id,
			channel_type = excluded.channel_type,
			address_key = excluded.address_key,
			address_json = excluded.address_json,
			agent_name = excluded.agent_name,
			workspace_dir = excluded.workspace_dir,
			branch_name = excluded.branch_name,
			status = excluded.status,
			updated_at = excluded.updated_at`,
		sessionID,
		chatID,
		topicID,
		channelType,
		addressKey,
		addressJSON,
		record.AgentName,
		record.WorkspaceDir,
		record.BranchName,
		record.Status,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("upsert relay session %q: %w", sessionID, err)
	}

	return nil
}

func (s *sqliteSessionStore) GetByAddress(ctx context.Context, channelType, addressKey string) (SessionRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT session_id, channel_type, address_key, address_json, agent_name, workspace_dir, branch_name, status
		FROM relay_session_metadata
		WHERE channel_type = ? AND address_key = ?`,
		strings.TrimSpace(channelType), strings.TrimSpace(addressKey),
	)

	var record SessionRecord
	if err := row.Scan(
		&record.SessionID,
		&record.ChannelType,
		&record.AddressKey,
		&record.AddressJSON,
		&record.AgentName,
		&record.WorkspaceDir,
		&record.BranchName,
		&record.Status,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionRecord{}, false, nil
		}
		return SessionRecord{}, false, fmt.Errorf("get relay session by address: %w", err)
	}

	return record, true, nil
}

func (s *sqliteSessionStore) GetBySessionID(ctx context.Context, sessionID string) (SessionRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT session_id, channel_type, address_key, address_json, agent_name, workspace_dir, branch_name, status
		FROM relay_session_metadata
		WHERE session_id = ?`,
		strings.TrimSpace(sessionID),
	)

	var record SessionRecord
	if err := row.Scan(
		&record.SessionID,
		&record.ChannelType,
		&record.AddressKey,
		&record.AddressJSON,
		&record.AgentName,
		&record.WorkspaceDir,
		&record.BranchName,
		&record.Status,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionRecord{}, false, nil
		}
		return SessionRecord{}, false, fmt.Errorf("get relay session by session_id: %w", err)
	}

	return record, true, nil
}

func (s *sqliteSessionStore) DeleteBySessionID(ctx context.Context, sessionID string) error {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM relay_session_metadata
		WHERE session_id = ?`,
		trimmed,
	); err != nil {
		return fmt.Errorf("delete relay session %q: %w", trimmed, err)
	}
	return nil
}

func (s *sqliteSessionStore) List(ctx context.Context) ([]SessionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, channel_type, address_key, address_json, agent_name, workspace_dir, branch_name, status
		FROM relay_session_metadata
		ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list relay sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]SessionRecord, 0)
	for rows.Next() {
		var record SessionRecord
		if err := rows.Scan(
			&record.SessionID,
			&record.ChannelType,
			&record.AddressKey,
			&record.AddressJSON,
			&record.AgentName,
			&record.WorkspaceDir,
			&record.BranchName,
			&record.Status,
		); err != nil {
			return nil, fmt.Errorf("scan relay session: %w", err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relay sessions: %w", err)
	}

	return out, nil
}

func telegramTuple(channelType, addressJSON string) (int64, int, error) {
	if channelType != ChannelTypeTelegram {
		return 0, 0, nil
	}

	var address struct {
		ChatID  int64 `json:"chat_id"`
		TopicID int   `json:"topic_id"`
	}
	if err := json.Unmarshal([]byte(addressJSON), &address); err != nil {
		return 0, 0, err
	}
	return address.ChatID, address.TopicID, nil
}
