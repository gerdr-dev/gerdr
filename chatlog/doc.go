// Package chatlog is the conversation event log that gerdr's chat UI (@gerdr/chat) renders: its format (Event,
// EVENTS.md), its file (File), the rules that turn ACP traffic into events (rules.go) and Session, a client-side
// driver for one ACP session that records it — send, interject, stop, withdraw, rewind, answer permission requests
// and questions, retry after network errors. gerdr's hub uses the same format and rules; programs embedding gerdr's
// agents (agents/native, agents/claude, agents/codex) use Session and push its events to @gerdr/chat themselves.
package chatlog
