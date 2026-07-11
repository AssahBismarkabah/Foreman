package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/foreman/foreman/internal/schemas"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	streamName       = "foreman-events"
	duplicatesWindow = 2 * time.Minute
	maxAge           = 14 * 24 * time.Hour
)

var streamSubjects = []string{
	"foreman.>",
}

type NATSBus struct {
	nc       *nats.Conn
	js       jetstream.JetStream
	stream   jetstream.Stream
	embedded *server.Server // set when running embedded, nil for external
}

// NewNATSBus connects to an external NATS server and configures JetStream.
func NewNATSBus(ctx context.Context, url string) (*NATSBus, error) {
	nc, err := nats.Connect(url,
		nats.Name("foreman"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(10),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	return newFromConn(ctx, nc)
}

// NewEmbeddedNATSBus starts an in-process NATS server for development.
func NewEmbeddedNATSBus(ctx context.Context) (*NATSBus, error) {
	opts := &server.Options{
		Port:              -1, // random port
		JetStream:         true,
		JetStreamMaxStore: 10 * 1024 * 1024 * 1024, // 10GB
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("embedded nats server: %w", err)
	}
	ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		ns.Shutdown()
		return nil, fmt.Errorf("embedded nats server not ready")
	}

	nc, err := nats.Connect(ns.ClientURL(), nats.Name("foreman-embedded"))
	if err != nil {
		ns.Shutdown()
		return nil, fmt.Errorf("connect to embedded nats: %w", err)
	}

	bus, err := newFromConn(ctx, nc)
	if err != nil {
		nc.Close()
		ns.Shutdown()
		return nil, err
	}
	bus.embedded = ns
	return bus, nil
}

func newFromConn(ctx context.Context, nc *nats.Conn) (*NATSBus, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", err)
	}

	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       streamName,
		Subjects:   streamSubjects,
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.LimitsPolicy,
		MaxAge:     maxAge,
		Duplicates: duplicatesWindow,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create stream: %w", err)
	}

	return &NATSBus{nc: nc, js: js, stream: stream}, nil
}

func (b *NATSBus) Publish(ctx context.Context, subject string, evt schemas.Event) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	// Use event ID as dedup key for exactly-once delivery
	_, err = b.js.Publish(ctx, subject, data,
		jetstream.WithMsgID(evt.ID),
	)
	if err != nil {
		return fmt.Errorf("jetstream publish: %w", err)
	}
	return nil
}

func (b *NATSBus) Subscribe(ctx context.Context, subject string, handler EventHandler) (func(), error) {
	consumer, err := b.stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       fmt.Sprintf("foreman-worker-%d", time.Now().UnixNano()),
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxAckPending: 100,
		MaxDeliver:    10,
		AckWait:       30 * time.Second,
		FilterSubject: subject,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		for {
			msgs, err := consumer.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					log.Printf("natsbus: fetch error: %v", err)
					time.Sleep(time.Second)
					continue
				}
			}

			for msg := range msgs.Messages() {
				var evt schemas.Event
				if err := json.Unmarshal(msg.Data(), &evt); err != nil {
					log.Printf("natsbus: unmarshal error: %v", err)
					if nakErr := msg.Nak(); nakErr != nil {
						log.Printf("natsbus: nak error: %v", nakErr)
					}
					continue
				}

				if err := handler(ctx, evt); err != nil {
					log.Printf("natsbus: handler error on %s: %v", subject, err)
					if nakErr := msg.Nak(); nakErr != nil {
						log.Printf("natsbus: nak error: %v", nakErr)
					}
					continue
				}

				if ackErr := msg.Ack(); ackErr != nil {
					log.Printf("natsbus: ack error: %v", ackErr)
				}
			}

			if msgs.Error() != nil {
				log.Printf("natsbus: fetch error: %v", msgs.Error())
			}
		}
	}()

	return cancel, nil
}

func (b *NATSBus) Close() error {
	b.nc.Close()
	if b.embedded != nil {
		b.embedded.Shutdown()
	}
	return nil
}
