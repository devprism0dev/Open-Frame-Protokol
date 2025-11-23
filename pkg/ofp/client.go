package ofp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/devprism0dev/Open-Frame-Protokol/internal/certs"
	"github.com/sirupsen/logrus"
)

// Client represents an OFP client
type Client struct {
	Addr              string
	Conn              *Conn
	Stream            *Stream
	connStreamMu      sync.RWMutex // Protects Conn and Stream fields
	Handler           func([]byte, *Stream)
	heartbeatInterval time.Duration
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup // WaitGroup for graceful shutdown
	lastPong          time.Time      // Track last pong received
	lastPongMu        sync.RWMutex   // Protects lastPong
	// Pub/Sub handlers
	TopicHandlers   map[string]func([]byte)
	topicHandlersMu sync.RWMutex
	// Event handlers
	EventHandlers   map[string]func([]byte)
	eventHandlersMu sync.RWMutex
}

// ClientConfig holds configuration for the client
type ClientConfig struct {
	Addr              string
	Handler           func([]byte, *Stream)
	HeartbeatInterval time.Duration
}

// NewClient creates a new OFP client
func NewClient(config ClientConfig) *Client {
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		Addr:              config.Addr,
		Handler:           config.Handler,
		heartbeatInterval: config.HeartbeatInterval,
		ctx:               ctx,
		cancel:            cancel,
		TopicHandlers:     make(map[string]func([]byte)),
		EventHandlers:     make(map[string]func([]byte)),
	}
}

// Connect establishes a connection to the server
func (c *Client) Connect(clientName string) error {
	logrus.WithFields(logrus.Fields{
		"component":   "client",
		"action":      "connecting",
		"server":      c.Addr,
		"client_name": clientName,
	}).Info("Connecting to OFP server...")

	tlsConfig, err := c.getTLSConfig(clientName)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"component": "client",
			"action":    "connect",
			"error":     err,
		}).Fatal("Failed to create TLS config")
		return fmt.Errorf("failed to create TLS config: %w", err)
	}

	conn, err := DialAddr(c.Addr, tlsConfig, nil)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"component": "client",
			"action":    "connect",
			"error":     err,
		}).Fatal("Failed to dial server")
		return fmt.Errorf("failed to dial: %w", err)
	}

	stream, err := conn.OpenStreamSync(c.ctx)
	if err != nil {
		conn.CloseWithError(0x01, "Failed to open stream")
		logrus.WithFields(logrus.Fields{
			"component": "client",
			"action":    "connect",
			"error":     err,
		}).Fatal("Failed to open stream")
		return fmt.Errorf("failed to open stream: %w", err)
	}

	c.connStreamMu.Lock()
	c.Conn = conn
	c.Stream = stream
	c.connStreamMu.Unlock()

	c.lastPongMu.Lock()
	c.lastPong = time.Now()
	c.lastPongMu.Unlock()

	logrus.WithFields(logrus.Fields{
		"component":   "client",
		"action":      "connected",
		"server":      c.Addr,
		"client_name": clientName,
	}).Info("Successfully connected to OFP server")

	c.wg.Add(2)
	go c.startHeartbeat()
	go c.readLoop()

	return nil
}

// SendData sends data to the server
func (c *Client) SendData(data []byte) error {
	c.connStreamMu.RLock()
	stream := c.Stream
	c.connStreamMu.RUnlock()

	if stream == nil {
		return fmt.Errorf("not connected")
	}

	frame := CreateDataFrame(data)
	_, err := stream.Write(frame)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"component": "client",
			"action":    "send_data",
			"error":     err,
		}).Error("Failed to send data")
		return err
	}

	logrus.WithFields(logrus.Fields{
		"component": "client",
		"action":    "send_data",
		"data_size": len(data),
		"data":      string(data),
	}).Debug("Sent message to server")
	return nil
}

// Close closes the connection
func (c *Client) Close() error {
	logrus.WithFields(logrus.Fields{
		"component": "client",
		"action":    "closing",
	}).Info("Closing client connection")

	// Cancel context to signal all goroutines to stop
	c.cancel()

	// Wait for goroutines to finish
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	// Wait with timeout
	select {
	case <-done:
		logrus.WithFields(logrus.Fields{
			"component": "client",
			"action":    "closing",
		}).Debug("All goroutines finished")
	case <-time.After(5 * time.Second):
		logrus.WithFields(logrus.Fields{
			"component": "client",
			"action":    "closing",
		}).Warn("Timeout waiting for goroutines to finish")
	}

	c.connStreamMu.Lock()
	stream := c.Stream
	conn := c.Conn
	c.Stream = nil
	c.Conn = nil
	c.connStreamMu.Unlock()

	if stream != nil {
		stream.Close()
	}
	if conn != nil {
		return conn.CloseWithError(0x00, "Client closing")
	}
	return nil
}

func (c *Client) readLoop() {
	defer c.wg.Done()

	buf := make([]byte, 4096)
	for {
		// Check context cancellation
		select {
		case <-c.ctx.Done():
			logrus.WithFields(logrus.Fields{
				"component": "client",
				"action":    "shutdown",
				"server":    c.Addr,
			}).Info("Stopping read loop due to client shutdown")
			return
		default:
		}

		c.connStreamMu.RLock()
		stream := c.Stream
		c.connStreamMu.RUnlock()

		if stream == nil {
			return
		}

		// Set read deadline to allow periodic context checks
		stream.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := stream.Read(buf)
		if err != nil {
			// Check if error is due to timeout (expected for periodic checks)
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			logrus.WithFields(logrus.Fields{
				"component": "client",
				"action":    "stream_read",
				"server":    c.Addr,
				"error":     err,
			}).Error("Stream read error, closing connection")
			// Close connection on read error
			c.connStreamMu.Lock()
			if c.Stream != nil {
				c.Stream.Close()
			}
			if c.Conn != nil {
				c.Conn.CloseWithError(0x01, "Read error")
			}
			c.Stream = nil
			c.Conn = nil
			c.connStreamMu.Unlock()
			return
		}

		if n == 0 {
			continue
		}

		frameType, payload := ParseFrame(buf[:n])

		switch frameType {
		case FrameTypePing:
			// Respond to ping with pong
			if _, err := stream.Write(CreatePongFrame()); err != nil {
				logrus.WithFields(logrus.Fields{
					"component": "client",
					"action":    "send_pong",
					"server":    c.Addr,
					"error":     err,
				}).Error("Failed to send pong response")
				// Close connection on write error
				c.connStreamMu.Lock()
				if c.Stream != nil {
					c.Stream.Close()
				}
				if c.Conn != nil {
					c.Conn.CloseWithError(0x01, "Write error")
				}
				c.Stream = nil
				c.Conn = nil
				c.connStreamMu.Unlock()
				return
			}
			logrus.WithFields(logrus.Fields{
				"component": "client",
				"action":    "ping_pong",
				"server":    c.Addr,
			}).Info("Responded to ping with pong")
		case FrameTypePong:
			// Received pong, connection is alive
			c.lastPongMu.Lock()
			c.lastPong = time.Now()
			c.lastPongMu.Unlock()
			logrus.WithFields(logrus.Fields{
				"component": "client",
				"action":    "heartbeat",
				"server":    c.Addr,
			}).Info("Received pong from server")
		case FrameTypeData:
			// Handle data frame
			if c.Handler != nil && len(payload) > 0 {
				logrus.WithFields(logrus.Fields{
					"component": "handler",
					"action":    "data_received",
					"data_size": len(payload),
					"data":      string(payload),
				}).Info("Received data from server")
				c.Handler(payload, stream)
			}
		case FrameTypePublish:
			// Handle published message from topic
			_, topic, data := ParseTopicFrame(buf[:n])
			if topic != "" {
				c.topicHandlersMu.RLock()
				handler := c.TopicHandlers[topic]
				c.topicHandlersMu.RUnlock()
				if handler != nil {
					handler(data)
				} else {
					logrus.WithFields(logrus.Fields{
						"component": "client",
						"action":    "topic_message",
						"topic":     topic,
					}).Debug("Received message for topic but no handler registered")
				}
			}
		case FrameTypeEmitEvent:
			// Handle emitted event
			_, eventName, data := ParseEventFrame(buf[:n])
			if eventName != "" {
				c.eventHandlersMu.RLock()
				handler := c.EventHandlers[eventName]
				c.eventHandlersMu.RUnlock()
				if handler != nil {
					handler(data)
				} else {
					logrus.WithFields(logrus.Fields{
						"component":  "client",
						"action":     "event_received",
						"event_name": eventName,
					}).Debug("Received event but no handler registered")
				}
			}
		default:
			logrus.WithFields(logrus.Fields{
				"component":  "client",
				"action":     "unknown_frame",
				"server":     c.Addr,
				"frame_type": fmt.Sprintf("0x%02x", frameType),
			}).Warn("Received unknown frame type from server")
		}
	}
}

func (c *Client) startHeartbeat() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			logrus.WithFields(logrus.Fields{
				"component": "client",
				"action":    "shutdown",
				"server":    c.Addr,
			}).Debug("Stopping heartbeat due to client shutdown")
			return
		case <-ticker.C:
			c.connStreamMu.RLock()
			stream := c.Stream
			c.connStreamMu.RUnlock()

			if stream == nil {
				return
			}

			// Check if server hasn't responded to pings (timeout detection)
			c.lastPongMu.RLock()
			timeSinceLastPong := time.Since(c.lastPong)
			c.lastPongMu.RUnlock()

			if timeSinceLastPong > c.heartbeatInterval*3 {
				logrus.WithFields(logrus.Fields{
					"component":            "client",
					"action":               "heartbeat_timeout",
					"server":               c.Addr,
					"time_since_last_pong": timeSinceLastPong.String(),
				}).Warn("Server heartbeat timeout detected, closing connection")
				// Close connection on timeout
				c.connStreamMu.Lock()
				if c.Stream != nil {
					c.Stream.Close()
				}
				if c.Conn != nil {
					c.Conn.CloseWithError(0x01, "Heartbeat timeout")
				}
				c.Stream = nil
				c.Conn = nil
				c.connStreamMu.Unlock()
				return
			}

			if _, err := stream.Write(CreatePingFrame()); err != nil {
				logrus.WithFields(logrus.Fields{
					"component": "client",
					"action":    "heartbeat",
					"server":    c.Addr,
					"error":     err,
				}).Error("Heartbeat ping failed, closing connection")
				// Close connection on heartbeat failure
				c.connStreamMu.Lock()
				if c.Stream != nil {
					c.Stream.Close()
				}
				if c.Conn != nil {
					c.Conn.CloseWithError(0x01, "Heartbeat failed")
				}
				c.Stream = nil
				c.Conn = nil
				c.connStreamMu.Unlock()
				return
			}
			logrus.WithFields(logrus.Fields{
				"component": "client",
				"action":    "heartbeat",
				"server":    c.Addr,
			}).Info("Sent heartbeat ping to server")
		}
	}
}

// LogFinished logs that the client finished sending messages
func (c *Client) LogFinished() {
	logrus.WithFields(logrus.Fields{
		"component": "client",
		"action":    "finished",
	}).Info("Client finished sending messages. Press Ctrl+C to exit.")
}

// Subscribe subscribes to a topic and sets a handler for messages
func (c *Client) Subscribe(topic string, handler func([]byte)) error {
	c.connStreamMu.RLock()
	stream := c.Stream
	c.connStreamMu.RUnlock()

	if stream == nil {
		return fmt.Errorf("not connected")
	}

	// Register handler
	c.topicHandlersMu.Lock()
	c.TopicHandlers[topic] = handler
	c.topicHandlersMu.Unlock()

	// Send subscribe frame
	frame := CreateTopicFrame(FrameTypeSubscribe, topic, nil)
	if _, err := stream.Write(frame); err != nil {
		c.topicHandlersMu.Lock()
		delete(c.TopicHandlers, topic)
		c.topicHandlersMu.Unlock()
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"component": "client",
		"action":    "subscribe",
		"topic":     topic,
	}).Info("Subscribed to topic")
	return nil
}

// Unsubscribe unsubscribes from a topic
func (c *Client) Unsubscribe(topic string) error {
	c.connStreamMu.RLock()
	stream := c.Stream
	c.connStreamMu.RUnlock()

	if stream == nil {
		return fmt.Errorf("not connected")
	}

	// Remove handler
	c.topicHandlersMu.Lock()
	delete(c.TopicHandlers, topic)
	c.topicHandlersMu.Unlock()

	// Send unsubscribe frame
	frame := CreateTopicFrame(FrameTypeUnsubscribe, topic, nil)
	if _, err := stream.Write(frame); err != nil {
		return fmt.Errorf("failed to unsubscribe: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"component": "client",
		"action":    "unsubscribe",
		"topic":     topic,
	}).Info("Unsubscribed from topic")
	return nil
}

// Publish publishes a message to a topic
func (c *Client) Publish(topic string, data []byte) error {
	c.connStreamMu.RLock()
	stream := c.Stream
	c.connStreamMu.RUnlock()

	if stream == nil {
		return fmt.Errorf("not connected")
	}

	frame := CreateTopicFrame(FrameTypePublish, topic, data)
	if _, err := stream.Write(frame); err != nil {
		return fmt.Errorf("failed to publish: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"component": "client",
		"action":    "publish",
		"topic":     topic,
		"data_size": len(data),
	}).Debug("Published message to topic")
	return nil
}

// On registers a handler for an event
func (c *Client) On(eventName string, handler func([]byte)) error {
	c.connStreamMu.RLock()
	stream := c.Stream
	c.connStreamMu.RUnlock()

	if stream == nil {
		return fmt.Errorf("not connected")
	}

	// Register handler
	c.eventHandlersMu.Lock()
	c.EventHandlers[eventName] = handler
	c.eventHandlersMu.Unlock()

	// Send on event frame
	frame := CreateEventFrame(FrameTypeOnEvent, eventName, nil)
	if _, err := stream.Write(frame); err != nil {
		c.eventHandlersMu.Lock()
		delete(c.EventHandlers, eventName)
		c.eventHandlersMu.Unlock()
		return fmt.Errorf("failed to register event listener: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"component":  "client",
		"action":     "on_event",
		"event_name": eventName,
	}).Info("Registered event listener")
	return nil
}

// Off unregisters a handler for an event
func (c *Client) Off(eventName string) error {
	c.connStreamMu.RLock()
	stream := c.Stream
	c.connStreamMu.RUnlock()

	if stream == nil {
		return fmt.Errorf("not connected")
	}

	// Remove handler
	c.eventHandlersMu.Lock()
	delete(c.EventHandlers, eventName)
	c.eventHandlersMu.Unlock()

	// Send off event frame
	frame := CreateEventFrame(FrameTypeOffEvent, eventName, nil)
	if _, err := stream.Write(frame); err != nil {
		return fmt.Errorf("failed to unregister event listener: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"component":  "client",
		"action":     "off_event",
		"event_name": eventName,
	}).Info("Unregistered event listener")
	return nil
}

// Emit emits an event with data
func (c *Client) Emit(eventName string, data []byte) error {
	c.connStreamMu.RLock()
	stream := c.Stream
	c.connStreamMu.RUnlock()

	if stream == nil {
		return fmt.Errorf("not connected")
	}

	frame := CreateEventFrame(FrameTypeEmitEvent, eventName, data)
	if _, err := stream.Write(frame); err != nil {
		return fmt.Errorf("failed to emit event: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"component":  "client",
		"action":     "emit_event",
		"event_name": eventName,
		"data_size":  len(data),
	}).Debug("Emitted event")
	return nil
}

func (c *Client) getTLSConfig(clientName string) (*tls.Config, error) {
	// Use server's CA certificate for verification
	return certs.GetClientTLSConfig(clientName)
}
