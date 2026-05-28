package actorlayer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultMailboxCapacity is the default in-memory mailbox capacity per actor.
	DefaultMailboxCapacity = 64
	// DefaultAskTimeout is the default timeout used by Ask when none is provided.
	DefaultAskTimeout = 30 * time.Second
)

// Clock abstracts time for scheduling and tests.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

// Config defines runtime limits and integrations for System.
type Config struct {
	NodeID                 string
	DefaultMailbox         MailboxFactory
	DeadLetters            DeadLetterSink
	Observer               Observer
	Tracer                 Tracer
	Supervision            SupervisorStrategy
	Clock                  Clock
	MaxActors              int
	MaxTotalQueued         int
	DefaultAskLimit        time.Duration
	TellRateLimitPerSecond int
	ShutdownPolicy         ShutdownPolicy
	ShutdownPollInterval   time.Duration
}

// ShutdownPolicy controls how Shutdown waits for in-flight work.
type ShutdownPolicy int

const (
	// ShutdownImmediate cancels actors immediately.
	ShutdownImmediate ShutdownPolicy = iota
	// ShutdownDrain waits until actors become idle before canceling.
	ShutdownDrain
)

// System hosts actor cells, routing, and runtime controls.
type System struct {
	cfg Config

	mu          sync.RWMutex
	actors      map[ActorID]*actorCell
	spawning    map[ActorID]struct{}
	subMu       sync.RWMutex
	subscribers map[string]map[ActorID]Ref

	ctx    context.Context
	cancel context.CancelFunc

	idSeq  atomic.Uint64
	askSeq atomic.Uint64

	shuttingDown atomic.Bool
	rateMu       sync.Mutex
	rateWindow   int64
	rateCount    int
}

type tellMode struct {
	allowDuringShutdown bool
	skipRateLimit       bool
	skipQueueLimit      bool
}

// Ref returns a local actor reference by id.
func (s *System) Ref(id ActorID) Ref {
	return Ref{id: id, sys: s}
}

type actorCell struct {
	sys   *System
	ref   Ref
	props Props

	mailbox    Mailbox
	supervisor SupervisorStrategy
	behavior   Behavior
	parent     *Ref
	state      *stateMap

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	watchers map[ActorID]Ref
	stopped  bool
	done     chan struct{}
	inFlight atomic.Int32
}

// NewSystem constructs an in-memory actor runtime with defaults.
func NewSystem(cfg Config) (*System, error) {
	if cfg.DefaultMailbox == nil {
		cfg.DefaultMailbox = NewBoundedMailboxFactory(BoundedMailboxConfig{
			Capacity:   DefaultMailboxCapacity,
			FullPolicy: FailFast,
		})
	}
	if cfg.DeadLetters == nil {
		cfg.DeadLetters = NopDeadLetterSink{}
	}
	if cfg.Observer == nil {
		cfg.Observer = NopObserver{}
	}
	if cfg.Tracer == nil {
		cfg.Tracer = NopTracer{}
	}
	if cfg.Supervision == nil {
		cfg.Supervision = DefaultSupervisor{}
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.DefaultAskLimit <= 0 {
		cfg.DefaultAskLimit = DefaultAskTimeout
	}
	if cfg.ShutdownPollInterval <= 0 {
		cfg.ShutdownPollInterval = 10 * time.Millisecond
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &System{
		cfg:      cfg,
		actors:   make(map[ActorID]*actorCell),
		spawning: make(map[ActorID]struct{}),
		ctx:      ctx,
		cancel:   cancel,
	}, nil
}

// Spawn creates and starts a new actor instance.
func (s *System) Spawn(ctx context.Context, name string, props Props, opts ...SpawnOption) (Ref, error) {
	traceCtx, span := s.cfg.Tracer.Start(ctx, "actor.spawn", TraceAttrs{"actor.name": name})
	ref, err := s.spawnInternal(traceCtx, name, props, nil, opts...)
	if err != nil {
		span.End(err)
		return Ref{}, err
	}
	span.AddEvent("actor.spawned", TraceAttrs{"actor.id": string(ref.ID())})
	span.End(nil)
	return ref, nil
}

func (s *System) spawnInternal(ctx context.Context, name string, props Props, parent *Ref, opts ...SpawnOption) (Ref, error) {
	if ctx.Err() != nil {
		return Ref{}, ctx.Err()
	}
	if s.shuttingDown.Load() {
		return Ref{}, ErrShuttingDown
	}
	if name == "" {
		return Ref{}, errors.New("actorlayer: actor name is required")
	}
	if props.NewBehavior == nil {
		return Ref{}, errors.New("actorlayer: props.NewBehavior is required")
	}

	var cfg spawnOptions
	for _, opt := range opts {
		opt.applySpawn(&cfg)
	}

	id := cfg.actorID
	if id == "" {
		id = fmt.Sprintf("%s-%d", name, s.idSeq.Add(1))
	}
	actorID := ActorID(id)

	s.mu.Lock()
	if _, exists := s.actors[actorID]; exists {
		s.mu.Unlock()
		return Ref{}, fmt.Errorf("actorlayer: actor already exists: %s", actorID)
	}
	if _, exists := s.spawning[actorID]; exists {
		s.mu.Unlock()
		return Ref{}, fmt.Errorf("actorlayer: actor already exists: %s", actorID)
	}
	if s.cfg.MaxActors > 0 && len(s.actors)+len(s.spawning) >= s.cfg.MaxActors {
		s.mu.Unlock()
		return Ref{}, errors.New("actorlayer: max actors limit reached")
	}
	s.spawning[actorID] = struct{}{}
	s.mu.Unlock()
	committed := false
	var mb Mailbox
	defer func() {
		if committed {
			return
		}
		if mb != nil {
			_ = mb.Close()
		}
		s.mu.Lock()
		delete(s.spawning, actorID)
		s.mu.Unlock()
	}()

	mailboxFactory := props.Mailbox
	if mailboxFactory == nil {
		mailboxFactory = s.cfg.DefaultMailbox
	}
	var err error
	mb, err = mailboxFactory.NewMailbox(actorID)
	if err != nil {
		return Ref{}, err
	}

	ref := Ref{id: actorID, sys: s}
	spawnCtx := SpawnContext{Self: ref, Parent: parent}
	behavior, err := props.NewBehavior(spawnCtx)
	if err != nil {
		return Ref{}, err
	}

	supervisor := props.Supervisor
	if supervisor == nil {
		supervisor = s.cfg.Supervision
	}

	cellCtx, cancel := context.WithCancel(s.ctx)
	cell := &actorCell{
		sys:        s,
		ref:        ref,
		props:      props,
		mailbox:    mb,
		supervisor: supervisor,
		behavior:   behavior,
		parent:     parent,
		state:      newStateMap(),
		ctx:        cellCtx,
		cancel:     cancel,
		watchers:   make(map[ActorID]Ref),
		done:       make(chan struct{}),
	}

	s.mu.Lock()
	delete(s.spawning, actorID)
	s.actors[actorID] = cell
	s.mu.Unlock()
	committed = true
	s.cfg.Observer.OnActorSpawn(actorID)
	_ = s.Publish(context.Background(), LifecycleTopic, Started{Actor: ref})

	go cell.run()
	return ref, nil
}

// Tell enqueues an asynchronous message to an actor.
func (s *System) Tell(ctx context.Context, to Ref, payload any, opts ...TellOption) (err error) {
	traceCtx, span := s.cfg.Tracer.Start(ctx, "actor.tell", TraceAttrs{"actor.id": string(to.ID())})
	defer func() {
		span.End(err)
	}()
	ctx = traceCtx

	var cfg tellOptions
	for _, opt := range opts {
		opt.applyTell(&cfg)
	}

	err = s.tellWithMode(ctx, to, payload, cfg, tellMode{})
	if err == nil {
		span.AddEvent("actor.mailbox.enqueue", TraceAttrs{"actor.id": string(to.ID())})
	}
	return err
}

func (s *System) tellSystem(ctx context.Context, to Ref, payload any, opts ...TellOption) error {
	var cfg tellOptions
	for _, opt := range opts {
		opt.applyTell(&cfg)
	}
	return s.tellWithMode(ctx, to, payload, cfg, tellMode{
		allowDuringShutdown: true,
		skipRateLimit:       true,
		skipQueueLimit:      true,
	})
}

func (s *System) tellWithMode(ctx context.Context, to Ref, payload any, cfg tellOptions, mode tellMode) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.shuttingDown.Load() && !mode.allowDuringShutdown {
		return ErrShuttingDown
	}
	if !to.validFor(s) {
		return ErrActorNotFound
	}

	env := Envelope{
		ID:            cfg.messageID,
		CorrelationID: cfg.correlationID,
		To:            to,
		Payload:       payload,
		ReplyTo:       cfg.replyTo,
		Headers:       cfg.headers,
		Deadline:      cfg.deadline,
		SentAt:        s.cfg.Clock.Now(),
	}
	if env.ID == "" {
		env.ID = nextMessageID()
	}
	if cfg.from != nil {
		env.From = *cfg.from
	}

	cell := s.getActor(to.ID())
	if cell == nil {
		s.emitDeadLetter(ctx, DeadLetter{
			Envelope: env,
			Reason:   "actor_not_found",
			Err:      ErrActorNotFound,
		})
		return ErrActorNotFound
	}

	if s.cfg.TellRateLimitPerSecond > 0 && !mode.skipRateLimit && !s.allowTell() {
		err := ErrRateLimited
		s.emitDeadLetter(ctx, DeadLetter{
			Envelope: env,
			Reason:   "rate_limited",
			Err:      err,
		})
		return err
	}

	if s.cfg.MaxTotalQueued > 0 && !mode.skipQueueLimit && s.totalQueued() >= s.cfg.MaxTotalQueued {
		err := ErrMailboxFull
		s.emitDeadLetter(ctx, DeadLetter{
			Envelope: env,
			Reason:   "system_queue_limit",
			Err:      err,
		})
		return err
	}

	if err := cell.mailbox.Enqueue(ctx, env); err != nil {
		s.emitDeadLetter(ctx, DeadLetter{
			Envelope: env,
			Reason:   "enqueue_failed",
			Err:      err,
		})
		return err
	}
	s.cfg.Observer.OnTellEnqueue(to.ID())

	return nil
}

// Ask sends a message and waits for a reply envelope.
func Ask(ctx context.Context, sys *System, to Ref, payload any, opts ...AskOption) (resp Envelope, err error) {
	if sys == nil {
		return Envelope{}, errors.New("actorlayer: system is nil")
	}
	traceCtx, span := sys.cfg.Tracer.Start(ctx, "actor.ask", TraceAttrs{"actor.id": string(to.ID())})
	defer func() {
		span.End(err)
	}()
	ctx = traceCtx

	var cfg askOptions
	cfg.timeout = sys.cfg.DefaultAskLimit
	for _, opt := range opts {
		opt.applyAsk(&cfg)
	}

	timeout := cfg.timeout
	if timeout <= 0 {
		timeout = sys.cfg.DefaultAskLimit
	}
	sys.cfg.Observer.OnAskStart(to.ID())
	defer func() {
		sys.cfg.Observer.OnAskDone(to.ID(), err)
	}()

	replyCh := make(chan Envelope, 1)
	replyID := fmt.Sprintf("__ask_reply_%d", sys.askSeq.Add(1))
	replyRef, err := sys.Spawn(ctx, replyID, Props{
		Kind: "ask-reply",
		NewBehavior: func(SpawnContext) (Behavior, error) {
			return ReceiveFunc(func(_ Context, env Envelope) error {
				select {
				case replyCh <- env:
				default:
				}
				return nil
			}), nil
		},
	}, WithActorID(replyID))
	if err != nil {
		return Envelope{}, err
	}
	defer func() {
		_ = sys.Stop(context.Background(), replyRef)
	}()

	tellOpts := make([]TellOption, 0, 6)
	tellOpts = append(tellOpts, WithReplyTo(replyRef))
	if cfg.from != nil {
		tellOpts = append(tellOpts, WithFrom(*cfg.from))
	}
	if cfg.correlationID != "" {
		tellOpts = append(tellOpts, WithCorrelationID(cfg.correlationID))
	}
	if cfg.messageID != "" {
		tellOpts = append(tellOpts, WithMessageID(cfg.messageID))
	}
	if !cfg.deadline.IsZero() {
		tellOpts = append(tellOpts, WithDeadline(cfg.deadline))
	}
	if len(cfg.headers) > 0 {
		tellOpts = append(tellOpts, WithHeaders(cfg.headers))
	}

	if tellErr := sys.Tell(ctx, to, payload, tellOpts...); tellErr != nil {
		return Envelope{}, tellErr
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case env := <-replyCh:
		if replyErr, ok := env.Payload.(error); ok && replyErr != nil {
			return Envelope{}, replyErr
		}
		return env, nil
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return Envelope{}, ErrAskTimeout
		}
		return Envelope{}, waitCtx.Err()
	}
}

// Stop requests actor termination and waits until it exits or context ends.
func (s *System) Stop(ctx context.Context, ref Ref) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !ref.validFor(s) {
		return ErrActorNotFound
	}
	cell := s.getActor(ref.ID())
	if cell == nil {
		return ErrActorNotFound
	}
	cell.stop()
	select {
	case <-cell.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Watch subscribes watcher to target termination notifications.
func (s *System) Watch(_ context.Context, watcher Ref, target Ref) error {
	if !watcher.validFor(s) || !target.validFor(s) {
		return ErrActorNotFound
	}
	watcherCell := s.getActor(watcher.ID())
	if watcherCell == nil {
		return ErrActorNotFound
	}
	targetCell := s.getActor(target.ID())
	if targetCell == nil {
		return ErrActorNotFound
	}
	targetCell.mu.Lock()
	targetCell.watchers[watcher.ID()] = watcher
	targetCell.mu.Unlock()
	return nil
}

// Shutdown stops the system according to the configured shutdown policy.
func (s *System) Shutdown(ctx context.Context) error {
	s.shuttingDown.Store(true)

	if s.cfg.ShutdownPolicy == ShutdownDrain {
		ticker := time.NewTicker(s.cfg.ShutdownPollInterval)
		defer ticker.Stop()
		for !s.allIdle() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}

	s.cancel()

	s.mu.RLock()
	cells := make([]*actorCell, 0, len(s.actors))
	for _, cell := range s.actors {
		cells = append(cells, cell)
	}
	s.mu.RUnlock()

	for _, cell := range cells {
		cell.stop()
	}

	for _, cell := range cells {
		select {
		case <-cell.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (s *System) getActor(id ActorID) *actorCell {
	s.mu.RLock()
	cell := s.actors[id]
	s.mu.RUnlock()
	return cell
}

func (s *System) totalQueued() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := 0
	for _, cell := range s.actors {
		total += cell.mailbox.Len()
	}
	return total
}

func (s *System) allIdle() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, cell := range s.actors {
		if cell.mailbox.Len() > 0 {
			return false
		}
		if cell.inFlight.Load() > 0 {
			return false
		}
	}
	return true
}

func (s *System) allowTell() bool {
	now := s.cfg.Clock.Now().Unix()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if s.rateWindow != now {
		s.rateWindow = now
		s.rateCount = 0
	}
	if s.rateCount >= s.cfg.TellRateLimitPerSecond {
		return false
	}
	s.rateCount++
	return true
}

func (s *System) emitDeadLetter(ctx context.Context, letter DeadLetter) {
	s.cfg.DeadLetters.HandleDeadLetter(ctx, letter)
	s.cfg.Observer.OnDeadLetter(letter)
}

func (cell *actorCell) run() {
	defer func() {
		cell.sys.removeActor(cell.ref.ID())
		close(cell.done)
	}()

	for {
		env, err := cell.mailbox.Dequeue(cell.ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, ErrMailboxClosed) {
				return
			}
			continue
		}
		cell.process(env)
	}
}

func (cell *actorCell) process(env Envelope) {
	cell.inFlight.Add(1)
	startedAt := cell.sys.cfg.Clock.Now()
	defer cell.inFlight.Add(-1)

	var sender *Ref
	if env.From.ID() != "" {
		s := env.From
		sender = &s
	}

	recvCtx := cell.ctx
	var cancel context.CancelFunc
	if !env.Deadline.IsZero() {
		recvCtx, cancel = context.WithDeadline(cell.ctx, env.Deadline)
		defer cancel()
	}

	recvCtx, span := cell.sys.cfg.Tracer.Start(recvCtx, "actor.receive", TraceAttrs{"actor.id": string(cell.ref.ID())})
	var spanErr error
	defer func() {
		span.End(spanErr)
	}()

	actx := &actorContext{
		Context: recvCtx,
		sys:     cell.sys,
		self:    cell.ref,
		parent:  cell.parent,
		sender:  sender,
		state:   cell.state,
	}

	var recvErr error
	var panicVal any

	func() {
		defer func() {
			if r := recover(); r != nil {
				panicVal = r
			}
		}()

		cell.mu.Lock()
		behavior := cell.behavior
		cell.mu.Unlock()

		recvErr = behavior.Receive(actx, env)
	}()

	if panicVal == nil && recvErr == nil {
		cell.sys.cfg.Observer.OnReceive(cell.ref.ID(), cell.sys.cfg.Clock.Now().Sub(startedAt), nil, nil)
		span.AddEvent("actor.receive.ok", nil)
		return
	}
	cell.sys.cfg.Observer.OnReceive(cell.ref.ID(), cell.sys.cfg.Clock.Now().Sub(startedAt), recvErr, panicVal)
	span.AddEvent("actor.supervision.failure", TraceAttrs{"actor.id": string(cell.ref.ID())})
	spanErr = recvErr
	if panicVal != nil && spanErr == nil {
		spanErr = fmt.Errorf("panic: %v", panicVal)
	}

	failure := Failure{
		Actor:   cell.ref,
		Message: env,
		Err:     recvErr,
		Panic:   panicVal,
		At:      cell.sys.cfg.Clock.Now(),
	}

	directive := cell.supervisor.Decide(SupervisionContext{System: cell.sys}, failure)
	switch directive {
	case Resume:
		return
	case Restart:
		cell.sys.cfg.Observer.OnRestart(cell.ref.ID())
		if err := cell.restart(); err != nil {
			cell.stop()
		}
	case Stop, Escalate:
		cell.stop()
	}
}

func (cell *actorCell) restart() error {
	spawnCtx := SpawnContext{Self: cell.ref, Parent: cell.parent}
	behavior, err := cell.props.NewBehavior(spawnCtx)
	if err != nil {
		return err
	}
	cell.mu.Lock()
	cell.behavior = behavior
	cell.mu.Unlock()
	return nil
}

func (cell *actorCell) stop() {
	cell.mu.Lock()
	if cell.stopped {
		cell.mu.Unlock()
		return
	}
	cell.stopped = true
	watchers := make([]Ref, 0, len(cell.watchers))
	for _, watcher := range cell.watchers {
		watchers = append(watchers, watcher)
	}
	cell.mu.Unlock()

	cell.cancel()
	_ = cell.mailbox.Close()
	cell.sys.cfg.Observer.OnActorStop(cell.ref.ID())
	_ = cell.sys.Publish(context.Background(), LifecycleTopic, Stopped{Actor: cell.ref})

	for _, watcher := range watchers {
		_ = cell.sys.tellSystem(context.Background(), watcher, Terminated{ActorID: cell.ref.ID()}, WithFrom(cell.ref))
	}
}

func (s *System) removeActor(id ActorID) {
	s.mu.Lock()
	delete(s.actors, id)
	s.mu.Unlock()
	s.pruneSubscriber(id)
}

func (s *System) pruneSubscriber(subscriber ActorID) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for topic, set := range s.subscribers {
		delete(set, subscriber)
		if len(set) == 0 {
			delete(s.subscribers, topic)
		}
	}
}
