package common

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

const EventExchange = "streamsphere.events"

type Event struct {
	EventID    string                 `json:"eventId"`
	EventType  string                 `json:"eventType"`
	OccurredAt string                 `json:"occurredAt"`
	Producer   string                 `json:"producer"`
	Data       map[string]interface{} `json:"data"`
}

func NewEvent(eventType, producer string, data map[string]interface{}) Event {
	return Event{
		EventID: uuid.NewString(), EventType: eventType,
		OccurredAt: time.Now().UTC().Format(time.RFC3339), Producer: producer, Data: data,
	}
}

type EventBus struct {
	URL string
}

func (b EventBus) Publish(ctx context.Context, event Event) error {
	if b.URL == "" {
		return nil
	}
	connection, err := amqp.Dial(b.URL)
	if err != nil {
		return err
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return err
	}
	defer channel.Close()
	if err := channel.ExchangeDeclare(EventExchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return channel.PublishWithContext(ctx, EventExchange, event.EventType, false, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: payload,
		MessageId: event.EventID, Timestamp: time.Now(),
	})
}

func (b EventBus) PublishBestEffort(event Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.Publish(ctx, event); err != nil {
		log.Printf("event publish skipped type=%s error=%v", event.EventType, err)
	}
}

func (b EventBus) StartConsumer(ctx context.Context, queue string, bindings []string, handler func(Event) error) {
	if b.URL == "" {
		return
	}
	go func() {
		for ctx.Err() == nil {
			if err := b.consumeOnce(ctx, queue, bindings, handler); err != nil {
				log.Printf("event consumer queue=%s error=%v; retrying", queue, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}
	}()
}

func (b EventBus) consumeOnce(ctx context.Context, queue string, bindings []string, handler func(Event) error) error {
	connection, err := amqp.Dial(b.URL)
	if err != nil {
		return err
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return err
	}
	defer channel.Close()
	if err := channel.ExchangeDeclare(EventExchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	declared, err := channel.QueueDeclare(queue, true, false, false, false, nil)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if err := channel.QueueBind(declared.Name, binding, EventExchange, false, nil); err != nil {
			return err
		}
	}
	messages, err := channel.Consume(declared.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case message, ok := <-messages:
			if !ok {
				return amqp.ErrClosed
			}
			var event Event
			if err := json.Unmarshal(message.Body, &event); err != nil {
				_ = message.Nack(false, false)
				continue
			}
			if err := handler(event); err != nil {
				log.Printf("event handler type=%s error=%v", event.EventType, err)
				_ = message.Nack(false, true)
				continue
			}
			_ = message.Ack(false)
		}
	}
}
