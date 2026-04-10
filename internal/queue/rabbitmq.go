package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/pkg/models"
	"github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

// Client wraps RabbitMQ connection and channel
type Client struct {
	conn    *amqp091.Connection
	channel *amqp091.Channel
	queue   amqp091.Queue
	url     string
}

// NewClient creates a new RabbitMQ client
func NewClient(url string) (*Client, error) {
	client := &Client{url: url}

	if err := client.connect(); err != nil {
		return nil, err
	}

	return client, nil
}

// connect establishes connection to RabbitMQ
func (c *Client) connect() error {
	var err error

	// Try to connect with retries
	for i := 0; i < 5; i++ {
		c.conn, err = amqp091.Dial(c.url)
		if err == nil {
			break
		}
		logrus.Warnf("Failed to connect to RabbitMQ (attempt %d/5): %v", i+1, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ after retries: %w", err)
	}

	c.channel, err = c.conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open channel: %w", err)
	}

	// Declare the main queue
	c.queue, err = c.channel.QueueDeclare(
		"analytics_messages", // name
		true,                 // durable
		false,                // delete when unused
		false,                // exclusive
		false,                // no-wait
		nil,                  // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	// Declare dead letter exchange and queue for failed messages
	err = c.channel.ExchangeDeclare(
		"analytics_dlx", // name
		"direct",        // type
		true,            // durable
		false,           // auto-deleted
		false,           // internal
		false,           // no-wait
		nil,             // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare DLX: %w", err)
	}

	_, err = c.channel.QueueDeclare(
		"analytics_messages_dlq", // name
		true,                     // durable
		false,                    // delete when unused
		false,                    // exclusive
		false,                    // no-wait
		nil,                      // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare DLQ: %w", err)
	}

	err = c.channel.QueueBind(
		"analytics_messages_dlq", // queue name
		"failed",                 // routing key
		"analytics_dlx",          // exchange
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to bind DLQ: %w", err)
	}

	logrus.Info("Successfully connected to RabbitMQ")
	return nil
}

// Publish sends a message to the queue
func (c *Client) Publish(ctx context.Context, msg *models.QueueMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	err = c.channel.PublishWithContext(
		ctx,
		"",           // exchange
		c.queue.Name, // routing key
		false,        // mandatory
		false,        // immediate
		amqp091.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp091.Persistent,
			Timestamp:    time.Now(),
		},
	)

	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

// Consume starts consuming messages from the queue
func (c *Client) Consume(handler func(*models.QueueMessage) error) error {
	msgs, err := c.channel.Consume(
		c.queue.Name, // queue
		"",           // consumer
		false,        // auto-ack (manual ack for reliability)
		false,        // exclusive
		false,        // no-local
		false,        // no-wait
		nil,          // args
	)
	if err != nil {
		return fmt.Errorf("failed to start consuming: %w", err)
	}

	logrus.Info("Started consuming messages from queue")

	for msg := range msgs {
		var queueMsg models.QueueMessage
		if err := json.Unmarshal(msg.Body, &queueMsg); err != nil {
			logrus.Errorf("Failed to unmarshal message: %v", err)
			msg.Nack(false, false) // Negative ack, don't requeue (send to DLQ)
			continue
		}

		// Process the message
		if err := handler(&queueMsg); err != nil {
			logrus.Errorf("Failed to process message %s: %v", queueMsg.MessageID, err)
			msg.Nack(false, false) // Send to DLQ
			continue
		}

		// Acknowledge successful processing
		if err := msg.Ack(false); err != nil {
			logrus.Errorf("Failed to ack message: %v", err)
		}
	}

	return nil
}

// Close closes the RabbitMQ connection
func (c *Client) Close() error {
	if c.channel != nil {
		c.channel.Close()
	}
	if c.conn != nil {
		c.conn.Close()
	}
	return nil
}
