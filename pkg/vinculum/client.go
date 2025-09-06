package vinculum

import "context"

type Client interface {
	Subscriber

	Connect(ctx context.Context) error
	Disconnect() error

	Subscribe(ctx context.Context, topicPattern string) error
	Unsubscribe(ctx context.Context, topicPattern string) error
	UnsubscribeAll(ctx context.Context) error

	Publish(ctx context.Context, topic string, payload any) error
	PublishSync(ctx context.Context, topic string, payload any) error
}
