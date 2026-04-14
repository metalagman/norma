package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Collaborator struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username,omitempty"`
	FirstName string    `json:"first_name,omitempty"`
	AddedBy   string    `json:"added_by"`
	AddedAt   time.Time `json:"added_at"`
}

type CollaboratorStore struct {
	db *sql.DB
}

func NewCollaboratorStore(db *sql.DB) *CollaboratorStore {
	return &CollaboratorStore{db: db}
}

func (s *CollaboratorStore) AddCollaborator(ctx context.Context, c Collaborator) error {
	if c.UserID == "" {
		return fmt.Errorf("user_id is required")
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO relay_collaborators (user_id, username, first_name, added_by, added_at)
		VALUES (?, ?, ?, ?, ?)`,
		c.UserID, c.Username, c.FirstName, c.AddedBy, c.AddedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("add collaborator: %w", err)
	}
	return nil
}

func (s *CollaboratorStore) RemoveCollaborator(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("user_id is required")
	}

	_, err := s.db.ExecContext(ctx, `
		DELETE FROM relay_collaborators
		WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("remove collaborator: %w", err)
	}
	return nil
}

func (s *CollaboratorStore) GetCollaborator(ctx context.Context, userID string) (*Collaborator, bool, error) {
	var username, firstName, addedBy, addedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT username, first_name, added_by, added_at
		FROM relay_collaborators
		WHERE user_id = ?`,
		userID,
	).Scan(&username, &firstName, &addedBy, &addedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get collaborator: %w", err)
	}

	parsedTime, err := time.Parse(time.RFC3339, addedAt)
	if err != nil {
		return nil, false, fmt.Errorf("parse added_at: %w", err)
	}

	return &Collaborator{
		UserID:    userID,
		Username:  username,
		FirstName: firstName,
		AddedBy:   addedBy,
		AddedAt:   parsedTime,
	}, true, nil
}

func (s *CollaboratorStore) ListCollaborators(ctx context.Context) ([]Collaborator, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, username, first_name, added_by, added_at
		FROM relay_collaborators
		ORDER BY added_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list collaborators: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var collaborators []Collaborator
	for rows.Next() {
		var c Collaborator
		var addedAt string
		if err := rows.Scan(&c.UserID, &c.Username, &c.FirstName, &c.AddedBy, &addedAt); err != nil {
			return nil, fmt.Errorf("scan collaborator: %w", err)
		}
		parsedTime, err := time.Parse(time.RFC3339, addedAt)
		if err != nil {
			return nil, fmt.Errorf("parse added_at: %w", err)
		}
		c.AddedAt = parsedTime
		collaborators = append(collaborators, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collaborators: %w", err)
	}

	return collaborators, nil
}
