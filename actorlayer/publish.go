package actorlayer

import (
	"context"
	"errors"
	"fmt"
)

// PublishedMessage wraps topic broadcasts delivered to subscribers.
type PublishedMessage struct {
	Topic   string
	Payload any
}

// Subscribe registers an actor to receive PublishedMessage values for a topic.
func (s *System) Subscribe(topic string, subscriber Ref) error {
	if topic == "" {
		return errors.New("actorlayer: topic is required")
	}
	if !subscriber.validFor(s) {
		return ErrActorNotFound
	}
	if s.getActor(subscriber.ID()) == nil {
		return ErrActorNotFound
	}

	s.subMu.Lock()
	defer s.subMu.Unlock()
	if s.subscribers == nil {
		s.subscribers = make(map[string]map[ActorID]Ref)
	}
	set := s.subscribers[topic]
	if set == nil {
		set = make(map[ActorID]Ref)
		s.subscribers[topic] = set
	}
	set[subscriber.ID()] = subscriber
	return nil
}

// Unsubscribe removes an actor subscription for a topic.
func (s *System) Unsubscribe(topic string, subscriber Ref) error {
	if topic == "" {
		return errors.New("actorlayer: topic is required")
	}
	if !subscriber.validFor(s) {
		return ErrActorNotFound
	}

	s.subMu.Lock()
	defer s.subMu.Unlock()
	set := s.subscribers[topic]
	if set == nil {
		return nil
	}
	delete(set, subscriber.ID())
	if len(set) == 0 {
		delete(s.subscribers, topic)
	}
	return nil
}

// Publish sends a payload to all current subscribers of a topic.
func (s *System) Publish(ctx context.Context, topic string, payload any) error {
	if topic == "" {
		return errors.New("actorlayer: topic is required")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	s.subMu.RLock()
	set := s.subscribers[topic]
	targets := make([]Ref, 0, len(set))
	for _, ref := range set {
		targets = append(targets, ref)
	}
	s.subMu.RUnlock()
	if len(targets) == 0 {
		return nil
	}

	msg := PublishedMessage{Topic: topic, Payload: payload}
	var publishErr error
	for _, target := range targets {
		if err := s.Tell(ctx, target, msg); err != nil {
			if publishErr == nil {
				publishErr = err
			} else {
				publishErr = fmt.Errorf("%w; %v", publishErr, err)
			}
		}
	}
	return publishErr
}
