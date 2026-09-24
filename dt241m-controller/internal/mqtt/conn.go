// Package mqtt owns everything on the broker side: the connection, topic layout,
// Home Assistant discovery payloads and routing of inbound commands.
package mqtt

import "context"

// MessageHandler receives inbound publishes in arrival order.
type MessageHandler func(topic string, payload []byte)

// Connection is the broker session as the controller sees it.
type Connection interface {
	Publish(ctx context.Context, topic string, payload string, retain bool) error
	Subscribe(ctx context.Context, topics []string) error
	OnMessage(handler MessageHandler)
	OnConnect(handler func())
	Connected() bool
	AwaitConnection(ctx context.Context) error
	Close(ctx context.Context) error
}

// Will is the retained Last Will published by the broker if the session dies uncleanly.
type Will struct {
	Topic   string
	Payload string
}
