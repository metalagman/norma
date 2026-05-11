package taskmaster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const defaultQueueDepth = 32

const (
	MessageKindJob          = "job"
	MessageKindProgress     = "progress"
	MessageKindNotification = "notification"
	MessageKindResult       = "result"
	MessageKindError        = "error"

	OutcomeStatusCompleted = "completed"
	OutcomeStatusFailed    = "failed"
	OutcomeStatusStopped   = "stopped"
)

type Message struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Kind      string         `json:"kind"`
	From      Locator        `json:"from"`
	To        Locator        `json:"to"`
	ParentID  string         `json:"parent_id,omitempty"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Outcome struct {
	Status   string
	Content  string
	Metadata map[string]any
	Err      error
}

type EmitFunc func(ctx context.Context, msg Message) error

type Node interface {
	Run(ctx context.Context, msg Message, emit EmitFunc) Outcome
}

type closeableNode interface {
	Close() error
}

type OutcomeRouter func(msg Message, outcome Outcome) []Message

type Config struct {
	Logger        *zerolog.Logger
	RootNodeID    string
	Nodes         map[string]Node
	Targets       []Target
	OutcomeRouter OutcomeRouter
}

type messageStatus string

const (
	messageStatusQueued     messageStatus = "queued"
	messageStatusRunning    messageStatus = "running"
	messageStatusCompleted  messageStatus = "completed"
	messageStatusDispatched messageStatus = "dispatched"
	messageStatusFailed     messageStatus = "failed"
	messageStatusDropped    messageStatus = "dropped"
)

type messageState struct {
	message    Message
	status     messageStatus
	outcome    Outcome
	errText    string
	startedAt  time.Time
	finishedAt time.Time
}

type executor struct {
	nodeID      string
	queue       <-chan *messageState
	node        Node
	coordinator *coordinator
	logger      zerolog.Logger
}

type coordinator struct {
	logger zerolog.Logger

	rootNodeID    string
	nodeIDs       map[string]struct{}
	targets       targetRegistry
	outcomeRouter OutcomeRouter
	requestStop   func()

	mu             sync.Mutex
	nextMessageSeq uint64
	parentSeq      map[string]uint64
	queues         map[string]chan *messageState
	dispatchQueue  chan *messageState
	shuttingDown   bool
	finalErr       error

	wg sync.WaitGroup
}

type Runtime struct {
	logger zerolog.Logger

	rootNodeID  string
	coordinator *coordinator

	nodeIDs []string
	nodes   map[string]Node

	startMu  sync.Mutex
	started  bool
	stopOnce sync.Once
	done     chan struct{}

	runCtx context.Context
	cancel context.CancelFunc

	errMu sync.Mutex
	err   error
}

func New(cfg Config) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	baseLogger := cfg.Logger
	if baseLogger == nil {
		logger := zerolog.Nop()
		baseLogger = &logger
	}
	logger := baseLogger.With().Logger()
	rootID := strings.ToLower(strings.TrimSpace(cfg.RootNodeID))

	coordinator, err := newCoordinator(logger, cfg)
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]Node, len(cfg.Nodes))
	nodeIDs := make([]string, 0, len(cfg.Nodes))
	for id, node := range cfg.Nodes {
		normalizedID := strings.ToLower(strings.TrimSpace(id))
		nodes[normalizedID] = node
		nodeIDs = append(nodeIDs, normalizedID)
	}

	runtime := &Runtime{
		logger:      logger,
		rootNodeID:  rootID,
		coordinator: coordinator,
		nodeIDs:     nodeIDs,
		nodes:       nodes,
		done:        make(chan struct{}),
	}
	coordinator.requestStop = runtime.requestStop
	return runtime, nil
}

func (r *Runtime) Start(ctx context.Context) error {
	r.startMu.Lock()
	defer r.startMu.Unlock()
	if r.started {
		return errors.New("runtime already started")
	}
	r.started = true
	r.runCtx, r.cancel = context.WithCancel(ctx)
	r.coordinator.start(r.runCtx, r.nodes)
	go r.shutdownOnContextDone()
	return nil
}

func (r *Runtime) Enqueue(msg Message) error {
	r.startMu.Lock()
	started := r.started
	r.startMu.Unlock()
	if !started {
		return errors.New("runtime is not started")
	}
	return r.coordinator.enqueue(context.Background(), msg, false)
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.startMu.Lock()
	started := r.started
	cancel := r.cancel
	done := r.done
	r.startMu.Unlock()
	if !started {
		return nil
	}
	r.coordinator.beginShutdown()
	if cancel != nil {
		cancel()
	}
	select {
	case <-done:
		return r.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) Done() <-chan struct{} {
	return r.done
}

func (r *Runtime) Err() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.err
}

func (r *Runtime) requestStop() {
	r.startMu.Lock()
	cancel := r.cancel
	r.startMu.Unlock()
	r.coordinator.beginShutdown()
	if cancel != nil {
		cancel()
	}
}

func (r *Runtime) shutdownOnContextDone() {
	<-r.runCtx.Done()
	r.stopOnce.Do(func() {
		r.coordinator.beginShutdown()
		r.coordinator.wait()
		r.setErr(r.coordinator.runtimeErr())
		closeErr := r.closeNodes()
		if closeErr != nil && r.Err() == nil {
			r.setErr(closeErr)
		}
		close(r.done)
	})
}

func (r *Runtime) closeNodes() error {
	var errs []string
	for _, id := range r.nodeIDs {
		node := r.nodes[id]
		closer, ok := node.(closeableNode)
		if !ok || closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) setErr(err error) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.RootNodeID) == "" {
		return errors.New("root node id is required")
	}
	if len(cfg.Nodes) == 0 {
		return errors.New("at least one node is required")
	}

	rootID := strings.ToLower(strings.TrimSpace(cfg.RootNodeID))
	hasRoot := false
	seenNodeIDs := make(map[string]string, len(cfg.Nodes))
	for nodeID, node := range cfg.Nodes {
		normalizedID := strings.ToLower(strings.TrimSpace(nodeID))
		if normalizedID == "" {
			return errors.New("node id is required")
		}
		if previousID, ok := seenNodeIDs[normalizedID]; ok {
			return fmt.Errorf("duplicate node ids %q and %q normalize to %q", previousID, nodeID, normalizedID)
		}
		seenNodeIDs[normalizedID] = nodeID
		if node == nil {
			return fmt.Errorf("node %q is nil", nodeID)
		}
		if normalizedID == rootID {
			hasRoot = true
		}
	}
	if !hasRoot {
		return fmt.Errorf("root node %q is missing", cfg.RootNodeID)
	}
	return nil
}

func newCoordinator(logger zerolog.Logger, cfg Config) (*coordinator, error) {
	rootID := strings.ToLower(strings.TrimSpace(cfg.RootNodeID))
	nodeIDs := make(map[string]struct{}, len(cfg.Nodes))
	queues := make(map[string]chan *messageState, len(cfg.Nodes))
	for nodeID := range cfg.Nodes {
		normalizedID := strings.ToLower(strings.TrimSpace(nodeID))
		nodeIDs[normalizedID] = struct{}{}
		queues[normalizedID] = make(chan *messageState, defaultQueueDepth)
	}

	return &coordinator{
		logger:        logger,
		rootNodeID:    rootID,
		nodeIDs:       nodeIDs,
		targets:       newTargetRegistry(cfg.Targets),
		outcomeRouter: cfg.OutcomeRouter,
		parentSeq:     make(map[string]uint64),
		queues:        queues,
		dispatchQueue: make(chan *messageState, defaultQueueDepth),
	}, nil
}

func (e *executor) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case next := <-e.queue:
			if next == nil {
				continue
			}
			if !e.coordinator.tryStartMessage(next) {
				continue
			}
			e.logger.Info().
				Str("node_id", e.nodeID).
				Str("message_id", next.message.ID).
				Str("session_id", next.message.SessionID).
				Str("kind", next.message.Kind).
				Str("message", next.message.Content).
				Msg("node received message")
			outcome := e.node.Run(ctx, next.message, e.coordinator.emitFunc(next))
			if outcome.Err != nil {
				event := e.logger.Info().
					Str("node_id", e.nodeID).
					Str("message_id", next.message.ID).
					Str("session_id", next.message.SessionID).
					Str("error", outcome.Err.Error())
				if e.coordinator.isExpectedShutdownCancel(outcome.Err) {
					event = e.logger.Debug().
						Str("node_id", e.nodeID).
						Str("message_id", next.message.ID).
						Str("session_id", next.message.SessionID).
						Str("error", outcome.Err.Error())
					event.Msg("node message canceled during shutdown")
				} else {
					event.Msg("node finished message")
				}
			} else {
				e.logger.Info().
					Str("node_id", e.nodeID).
					Str("message_id", next.message.ID).
					Str("session_id", next.message.SessionID).
					Str("result", strings.TrimSpace(outcome.Content)).
					Msg("node finished message")
			}
			e.coordinator.handleNodeOutcome(context.Background(), next, outcome)
		}
	}
}

func (c *coordinator) start(ctx context.Context, nodes map[string]Node) {
	for nodeID, node := range nodes {
		exec := &executor{
			nodeID:      nodeID,
			queue:       c.queues[nodeID],
			node:        node,
			coordinator: c,
			logger:      c.logger.With().Str("node_id", nodeID).Logger(),
		}
		c.wg.Add(1)
		go func(e *executor) {
			defer c.wg.Done()
			e.run(ctx)
		}(exec)
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.runDispatchLoop(ctx)
	}()
}

func (c *coordinator) wait() {
	c.wg.Wait()
}

func (c *coordinator) emitFunc(parent *messageState) EmitFunc {
	return func(ctx context.Context, msg Message) error {
		if strings.TrimSpace(msg.SessionID) == "" {
			msg.SessionID = parent.message.SessionID
		}
		if isZeroLocator(msg.From) {
			msg.From = parent.message.To
		}
		if strings.TrimSpace(msg.ParentID) == "" {
			msg.ParentID = parent.message.ID
		}
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any, 1)
		}
		return c.enqueue(ctx, msg, isBestEffortKind(msg.Kind))
	}
}

func (c *coordinator) enqueue(ctx context.Context, msg Message, bestEffort bool) error {
	msg, err := NormalizeMessage(msg)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		return errors.New("runtime is stopping")
	}
	if strings.TrimSpace(msg.ID) == "" {
		c.nextMessageSeq++
		msg.ID = fmt.Sprintf("msg-%d", c.nextMessageSeq)
	}
	if msg.ParentID != "" {
		c.parentSeq[msg.ParentID]++
		msg.Metadata = cloneMetadata(msg.Metadata)
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any, 1)
		}
		msg.Metadata["sequence"] = c.parentSeq[msg.ParentID]
	}
	queued := &messageState{
		message: msg,
		status:  messageStatusQueued,
	}
	queue, err := c.resolveQueueLocked(queued.message.To)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.logMessageEvent("message enqueued", queued)
	if bestEffort {
		select {
		case queue <- queued:
		default:
			queued.status = messageStatusDropped
			c.logMessageEvent("message dropped because destination queue is full", queued)
		}
		return nil
	}
	select {
	case queue <- queued:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NormalizeMessage(msg Message) (Message, error) {
	normalized := Message{
		ID:        strings.TrimSpace(msg.ID),
		SessionID: strings.TrimSpace(msg.SessionID),
		Kind:      strings.ToLower(strings.TrimSpace(msg.Kind)),
		ParentID:  strings.TrimSpace(msg.ParentID),
		Content:   msg.Content,
		Metadata:  cloneMetadata(msg.Metadata),
	}
	if normalized.SessionID == "" {
		return Message{}, errors.New("message.session_id is required")
	}
	if normalized.Kind == "" {
		return Message{}, errors.New("message.kind is required")
	}
	if !isKnownMessageKind(normalized.Kind) {
		return Message{}, fmt.Errorf("unsupported message.kind %q", msg.Kind)
	}
	if isZeroLocator(msg.From) {
		return Message{}, errors.New("message.from is required")
	}
	from, err := NormalizeLocator(msg.From)
	if err != nil {
		return Message{}, fmt.Errorf("message.from: %w", err)
	}
	to, err := NormalizeLocator(msg.To)
	if err != nil {
		return Message{}, fmt.Errorf("message.to: %w", err)
	}
	if isBuiltInSourceLocator(to) {
		return Message{}, fmt.Errorf("source locator %s cannot be used as a target", to)
	}
	normalized.From = from
	normalized.To = to
	return normalized, nil
}

func isKnownMessageKind(kind string) bool {
	switch kind {
	case MessageKindJob, MessageKindProgress, MessageKindNotification, MessageKindResult, MessageKindError:
		return true
	default:
		return false
	}
}

func isBestEffortKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case MessageKindProgress, MessageKindNotification:
		return true
	default:
		return false
	}
}

func normalizeOutcome(outcome Outcome) Outcome {
	outcome.Status = strings.ToLower(strings.TrimSpace(outcome.Status))
	if outcome.Err != nil {
		outcome.Status = OutcomeStatusFailed
	}
	if outcome.Status == "" {
		outcome.Status = OutcomeStatusCompleted
	}
	switch outcome.Status {
	case OutcomeStatusCompleted, OutcomeStatusFailed, OutcomeStatusStopped:
	default:
		outcome.Err = fmt.Errorf("unsupported outcome status %q", outcome.Status)
		outcome.Status = OutcomeStatusFailed
	}
	outcome.Content = strings.TrimSpace(outcome.Content)
	outcome.Metadata = cloneMetadata(outcome.Metadata)
	return outcome
}

func (c *coordinator) beginShutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shuttingDown = true
}

func (c *coordinator) runtimeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finalErr
}

func (c *coordinator) setFinalErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finalErr != nil {
		return
	}
	c.finalErr = err
	c.shuttingDown = true
}

func (c *coordinator) resolveQueueLocked(locator Locator) (chan *messageState, error) {
	if c.isLocalNodeTarget(locator) {
		queue, ok := c.queues[locator.Key]
		if !ok {
			return nil, fmt.Errorf("unknown local node locator.key %q", locator.Key)
		}
		return queue, nil
	}
	if c.targets.supports(locator) {
		return c.dispatchQueue, nil
	}
	return nil, fmt.Errorf("unsupported target locator %s", locator)
}

func (c *coordinator) isLocalNodeTarget(locator Locator) bool {
	if locator.Class != LocatorClassAgent || locator.Transport != LocatorTransportLocal {
		return false
	}
	_, ok := c.nodeIDs[locator.Key]
	return ok
}

func (c *coordinator) runDispatchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case next := <-c.dispatchQueue:
			if next == nil {
				continue
			}
			if !c.tryStartMessage(next) {
				continue
			}
			if err := c.targets.dispatchMessage(ctx, next.message); err != nil {
				c.handleDispatchFailure(context.Background(), next, err)
				continue
			}
			c.handleDispatchHandoff(next)
		}
	}
}

func (c *coordinator) handleDispatchHandoff(dispatched *messageState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dispatched.finishedAt = time.Now().UTC()
	dispatched.status = messageStatusDispatched
	c.logMessageEvent("message dispatched", dispatched)
}

func (c *coordinator) handleDispatchFailure(ctx context.Context, failed *messageState, err error) {
	c.mu.Lock()
	failed.finishedAt = time.Now().UTC()
	failed.status = messageStatusFailed
	failed.errText = err.Error()
	c.logMessageEvent("message dispatch failed", failed)
	shouldReport := failed.message.Kind == MessageKindJob && !isZeroLocator(failed.message.From) && !isBuiltInSourceLocator(failed.message.From)
	c.mu.Unlock()

	if !shouldReport {
		return
	}
	errorMsg := Message{
		SessionID: failed.message.SessionID,
		Kind:      MessageKindError,
		From:      failed.message.To,
		To:        failed.message.From,
		ParentID:  failed.message.ID,
		Content:   err.Error(),
	}
	if enqueueErr := c.enqueue(ctx, errorMsg, false); enqueueErr != nil {
		c.setFinalErr(fmt.Errorf("enqueue dispatch error message: %w", enqueueErr))
		if c.requestStop != nil {
			c.requestStop()
		}
	}
}

func (c *coordinator) tryStartMessage(next *messageState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shuttingDown {
		next.status = messageStatusDropped
		c.logMessageEvent("message skipped during shutdown", next)
		return false
	}
	now := time.Now().UTC()
	next.startedAt = now
	next.status = messageStatusRunning
	c.logMessageEvent("message started", next)
	return true
}

func (c *coordinator) handleNodeOutcome(ctx context.Context, done *messageState, outcome Outcome) {
	outcome = normalizeOutcome(outcome)

	c.mu.Lock()
	done.finishedAt = time.Now().UTC()
	done.outcome = outcome
	if outcome.Err != nil || outcome.Status == OutcomeStatusFailed {
		done.status = messageStatusFailed
		if outcome.Err != nil {
			done.errText = outcome.Err.Error()
		}
		c.logMessageEvent("message failed", done)
	} else {
		done.status = messageStatusCompleted
		c.logMessageEvent("message completed", done)
	}
	rootMessage := done.message.To.Key == c.rootNodeID
	shuttingDown := c.shuttingDown
	router := c.outcomeRouter
	c.mu.Unlock()

	if !shuttingDown && router != nil {
		for _, routed := range router(done.message, outcome) {
			routed = normalizeRoutedOutcomeMessage(done.message, outcome, routed)
			if err := c.enqueue(ctx, routed, isBestEffortKind(routed.Kind)); err != nil {
				c.setFinalErr(fmt.Errorf("enqueue routed outcome: %w", err))
				if c.requestStop != nil {
					c.requestStop()
				}
				return
			}
		}
	}
	if rootMessage && outcome.Err != nil {
		c.setFinalErr(fmt.Errorf("%s message failed: %w", c.rootNodeID, outcome.Err))
		if c.requestStop != nil {
			c.requestStop()
		}
		return
	}
}

func normalizeRoutedOutcomeMessage(parent Message, outcome Outcome, routed Message) Message {
	if strings.TrimSpace(routed.SessionID) == "" {
		routed.SessionID = parent.SessionID
	}
	if strings.TrimSpace(routed.Kind) == "" {
		if outcome.Err != nil || outcome.Status == OutcomeStatusFailed {
			routed.Kind = MessageKindError
		} else {
			routed.Kind = MessageKindResult
		}
	}
	if isZeroLocator(routed.From) {
		routed.From = parent.To
	}
	if strings.TrimSpace(routed.ParentID) == "" {
		routed.ParentID = parent.ID
	}
	return routed
}

func (c *coordinator) logMessageEvent(message string, next *messageState) {
	event := c.logger.Debug().
		Str("message_id", next.message.ID).
		Str("session_id", next.message.SessionID).
		Str("kind", next.message.Kind).
		Str("from", next.message.From.String()).
		Str("to", next.message.To.String()).
		Str("status", string(next.status))
	if next.message.ParentID != "" {
		event = event.Str("parent_id", next.message.ParentID)
	}
	if next.outcome.Content != "" {
		event = event.Str("output", next.outcome.Content)
	}
	if next.errText != "" {
		event = event.Str("error", next.errText)
	}
	event.Msg(message)
}

func (c *coordinator) isExpectedShutdownCancel(err error) bool {
	if !errors.Is(err, context.Canceled) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shuttingDown
}

func isZeroLocator(locator Locator) bool {
	return locator.Class == "" && locator.Transport == "" && locator.Key == "" && len(locator.Address) == 0
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}
