// Package poolagent provides ordered failover across multiple agent backends.
//
// A [PoolExecutor] lazily creates the first healthy member from a configured
// list and reuses it until a run fails. [NewPoolAgent] wraps that behavior in
// an ADK agent so callers can expose failover pools through the same interface
// as any other runtime agent.
package poolagent
