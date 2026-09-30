// Package larkweb marks chats read through the Feishu web client's gateway.
//
// Feishu's OpenAPI has no mark-read call, so larkim's other lever is an
// applink that walks the desktop client onto a chat and lets the client send
// the receipt itself (see the applink package). The web client does have one:
// PutReadMessages, whose maxPosition is a watermark, so a single request
// settles every unread message in a chat up to that position and answers with
// a status rather than leaving the outcome to be inferred from a later poll.
//
// This is the one path in larkim that reaches Feishu without lark-cli, because
// lark-cli speaks the OpenAPI and this call does not exist there. Authentication
// is the web session, read out of a browser's cookie jar at call time and never
// persisted: larkim keeps no credentials and the jar stays the browser's.
//
// Cookie values never reach a log, an error string or Sentry. A session cookie
// is a bearer credential, which makes it the one carve-out from the
// everything-goes telemetry rule in CLAUDE.md; cookie names and expiry answer
// every diagnostic question the values would.
package larkweb
