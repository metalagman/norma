package actorlayer

// LifecycleTopic is the pub/sub topic used for actor lifecycle notifications.
const LifecycleTopic = "actor.lifecycle"

// Started is emitted when an actor cell is created and registered.
type Started struct {
	Actor Ref
}

// Stopped is emitted when an actor begins shutdown.
type Stopped struct {
	Actor Ref
}

// Terminated is sent to watchers after an actor fully exits.
type Terminated struct {
	ActorID ActorID
}
