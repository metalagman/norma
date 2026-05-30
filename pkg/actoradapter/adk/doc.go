// Package adk adapts Google ADK agents to actorlayer behaviors.
//
// This package is an optional adapter. It is the only public actor adapter
// package in Norma that imports Google ADK for actor execution. Actorlayer core
// packages remain independent of ADK and expose provider-neutral refs,
// envelopes, behaviors, delivery hooks, and runtime events.
//
// Products decide whether and how to use this adapter. Product-owned code keeps
// provider configuration, session policy, tools, command delivery, retry/DLQ
// policy, telemetry, and persistence side effects outside actorlayer core.
package adk
