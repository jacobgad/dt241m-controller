package mqtt

import "context"

type MessageHandler func(topic string, payload []byte)

type Connection interface {
	Publish(ctx context.Context, topic string, payload string, retain bool) error
	Subscribe(ctx context.Context, topics []string) error
	OnMessage(handler MessageHandler)
	OnConnect(handler func())
	Connected() bool
	Close(ctx context.Context) error
}
