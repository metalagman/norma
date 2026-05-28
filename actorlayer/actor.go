package actorlayer

// Behavior handles one envelope at a time for an actor cell.
type Behavior interface {
	Receive(ctx Context, env Envelope) error
}

// ReceiveFunc adapts a function into a Behavior.
type ReceiveFunc func(ctx Context, env Envelope) error

// Receive executes the wrapped function.
func (f ReceiveFunc) Receive(ctx Context, env Envelope) error {
	return f(ctx, env)
}

// SpawnContext contains immutable context available while creating behavior.
type SpawnContext struct {
	Self   Ref
	Parent *Ref
}

// Props configures actor behavior construction and runtime policy.
type Props struct {
	Kind        string
	NewBehavior func(ctx SpawnContext) (Behavior, error)

	Mailbox    MailboxFactory
	Supervisor SupervisorStrategy
}
