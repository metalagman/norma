// Package taskmaster provides an unstable reusable async message scheduler.
//
// The runtime coordinates local node inboxes and external dispatch targets
// around one public Message type. Nodes receive one message, can emit many
// addressed messages while running, and return one terminal Outcome.
package taskmaster
