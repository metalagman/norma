// Package taskmaster provides an unstable reusable async task runtime.
//
// The runtime coordinates local agent tasks and external dispatch targets around
// one public Task type. Task completion is reported as another Task addressed
// to report_to instead of a separate result envelope.
package taskmaster
