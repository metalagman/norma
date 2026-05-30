package actorlayer

import "errors"

var (
	// ErrMailboxFull indicates a bounded mailbox rejected an enqueue because it is full.
	ErrMailboxFull = errors.New("actorlayer: mailbox full")
	// ErrMailboxClosed indicates a mailbox is no longer accepting messages.
	ErrMailboxClosed = errors.New("actorlayer: mailbox closed")
	// ErrMailboxDrop indicates a mailbox drop policy discarded a message.
	ErrMailboxDrop = errors.New("actorlayer: message dropped")
	// ErrActorNotFound indicates the target actor reference is unknown to the system.
	ErrActorNotFound = errors.New("actorlayer: actor not found")
	// ErrAskTimeout indicates Ask did not receive a reply before timeout.
	ErrAskTimeout = errors.New("actorlayer: ask timeout")
	// ErrRateLimited indicates the system rejected a Tell due to rate limiting.
	ErrRateLimited = errors.New("actorlayer: rate limited")
	// ErrScheduleCanceled indicates a scheduled send was canceled before delivery.
	ErrScheduleCanceled = errors.New("actorlayer: scheduled delivery canceled")
	// ErrShuttingDown indicates the system is shutting down and not accepting new work.
	ErrShuttingDown = errors.New("actorlayer: shutting down")
	// ErrUnhandled indicates a behavior intentionally did not handle the payload.
	ErrUnhandled = errors.New("actorlayer: unhandled message")
)
