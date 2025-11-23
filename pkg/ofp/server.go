package ofp

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/devprism0dev/Open-Frame-Protokol/pkg/certs"
	"github.com/sirupsen/logrus"
)

// ClientInfo holds information about a connected client
type ClientInfo struct {
	CN          string
	ConnectedAt time.Time
	LastPong    time.Time
	State       string // "connected", "disconnecting", "disconnected"
}

// Server represents an OFP server
type Server struct {
	Addr              string
	Handler           func([]byte, *Stream)
	Clients           map[*Stream]struct{}
	clientsMu         sync.RWMutex // Protects Clients map
	heartbeatInterval time.Duration
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup // WaitGroup for graceful shutdown
	listener          Listener
	listenerMu        sync.RWMutex
	metrics           *Metrics
	metricsLogPath    string
	// Pub/Sub topic management
	topics   map[string]map[*Stream]struct{}
	topicsMu sync.RWMutex
	// Event listener management
	eventListeners   map[string]map[*Stream]struct{}
	eventListenersMu sync.RWMutex
	// Client disconnect tracking
	clientInfo   map[*Stream]*ClientInfo
	clientInfoMu sync.RWMutex
	// Client disconnect callback
	OnClientDisconnect func(clientCN string, reason string)
}

// ServerConfig holds configuration for the server
type ServerConfig struct {
	Addr              string
	Handler           func([]byte, *Stream)
	HeartbeatInterval time.Duration
	ShowBanner        bool   // Show startup banner
	LogoPath          string // Path to logo image (optional)
	MetricsLogPath    string // Path to metrics log file (e.g., "metrics.log")
	// Client disconnect callback (optional)
	OnClientDisconnect func(clientCN string, reason string)
}

// NewServer creates a new OFP server
func NewServer(config ServerConfig) *Server {
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 5 * time.Second
	}

	// Show banner if enabled
	if config.ShowBanner {
		printServerBanner(config.Addr, config.LogoPath)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Set default metrics log path if not provided
	metricsLogPath := config.MetricsLogPath
	if metricsLogPath == "" {
		metricsLogPath = "metrics.log"
	}

	return &Server{
		Addr:               config.Addr,
		Handler:            config.Handler,
		Clients:            make(map[*Stream]struct{}),
		heartbeatInterval:  config.HeartbeatInterval,
		ctx:                ctx,
		cancel:             cancel,
		metrics:            NewMetrics(),
		metricsLogPath:     metricsLogPath,
		topics:             make(map[string]map[*Stream]struct{}),
		eventListeners:     make(map[string]map[*Stream]struct{}),
		clientInfo:         make(map[*Stream]*ClientInfo),
		OnClientDisconnect: config.OnClientDisconnect,
	}
}

// Listen starts listening for connections
func (s *Server) Listen() error {
	tlsConfig, err := s.getTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to create TLS config: %w", err)
	}

	listener, err := ListenAddr(s.Addr, tlsConfig, nil)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	s.listenerMu.Lock()
	s.listener = listener
	s.listenerMu.Unlock()

	logrus.WithFields(logrus.Fields{
		"component": "server",
		"address":   s.Addr,
		"protocol":  "OFP",
	}).Info("Server started and listening for connections")

	// Start metrics logger
	s.wg.Add(1)
	go s.startMetricsLogger()

	// Start accept loop in goroutine
	s.wg.Add(1)
	go s.acceptLoop()

	// Wait for context cancellation
	<-s.ctx.Done()
	return nil
}

// acceptLoop handles accepting new connections
func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		// Check if context is cancelled
		select {
		case <-s.ctx.Done():
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "shutdown",
			}).Info("Stopping accept loop")
			return
		default:
		}

		s.listenerMu.RLock()
		listener := s.listener
		s.listenerMu.RUnlock()

		if listener == nil {
			return
		}

		// Use context with timeout for accept
		acceptCtx, cancel := context.WithTimeout(s.ctx, 1*time.Second)
		sess, err := listener.Accept(acceptCtx)
		cancel()

		if err != nil {
			// Check if error is due to context cancellation
			if s.ctx.Err() != nil {
				return
			}
			// Check if error is due to timeout (expected)
			if err == context.DeadlineExceeded {
				continue
			}
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"error":     err,
			}).Error("Failed to accept new connection")
			s.metrics.RecordError("server", "accept", err.Error(), "", "error")
			continue
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleSession(sess)
		}()
	}
}

func (s *Server) handleSession(sess *Conn) {
	tlsState := sess.ConnectionState().TLS

	if len(tlsState.PeerCertificates) == 0 {
		logrus.WithFields(logrus.Fields{
			"component": "server",
			"action":    "connection_rejected",
			"reason":    "no_certificate",
		}).Warn("Client connection rejected: no certificate provided")
		s.metrics.IncrementRejectedConnections()
		s.metrics.RecordError("server", "connection_rejected", "no certificate provided", "", "warn")
		sess.CloseWithError(0x01, "Certificate required")
		return
	}

	clientCert := tlsState.PeerCertificates[0]
	clientCN := clientCert.Subject.CommonName
	clientSerial := clientCert.SerialNumber.String()

	logrus.WithFields(logrus.Fields{
		"component":   "server",
		"action":      "client_connected",
		"client_cn":   clientCN,
		"cert_serial": clientSerial,
		"remote_addr": sess.RemoteAddr().String(),
	}).Info("New client connection established")

	s.metrics.IncrementTotalConnections()

	stream, err := sess.AcceptStream(s.ctx)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"component": "server",
			"action":    "accept_stream",
			"client_cn": clientCN,
			"error":     err,
		}).Error("Failed to accept stream from client")
		s.metrics.RecordError("server", "accept_stream", err.Error(), clientCN, "error")
		s.metrics.DecrementActiveConnections()
		return
	}

	s.clientsMu.Lock()
	s.Clients[stream] = struct{}{}
	clientCount := len(s.Clients)
	s.clientsMu.Unlock()

	// Store client info for disconnect tracking
	now := time.Now()
	clientInfo := &ClientInfo{
		CN:          clientCN,
		ConnectedAt: now,
		LastPong:    now,
		State:       "connected",
	}
	s.clientInfoMu.Lock()
	s.clientInfo[stream] = clientInfo
	s.clientInfoMu.Unlock()

	logrus.WithFields(logrus.Fields{
		"component":     "server",
		"action":        "client_registered",
		"client_cn":     clientCN,
		"total_clients": clientCount,
	}).Debug("Client registered in server")

	defer func() {
		// Get client info before cleanup
		s.clientInfoMu.Lock()
		info := s.clientInfo[stream]
		delete(s.clientInfo, stream)
		s.clientInfoMu.Unlock()

		// Remove from all topics
		s.topicsMu.Lock()
		for topic, subscribers := range s.topics {
			delete(subscribers, stream)
			if len(subscribers) == 0 {
				delete(s.topics, topic)
			}
		}
		s.topicsMu.Unlock()

		// Remove from all event listeners
		s.eventListenersMu.Lock()
		for eventName, listeners := range s.eventListeners {
			delete(listeners, stream)
			if len(listeners) == 0 {
				delete(s.eventListeners, eventName)
			}
		}
		s.eventListenersMu.Unlock()

		s.clientsMu.Lock()
		delete(s.Clients, stream)
		remainingClients := len(s.Clients)
		s.clientsMu.Unlock()

		s.metrics.DecrementActiveConnections()

		disconnectReason := "read_error"
		if info != nil && info.State != "connected" {
			disconnectReason = info.State
		}

		logrus.WithFields(logrus.Fields{
			"component":         "server",
			"action":            "client_disconnected",
			"client_cn":         clientCN,
			"remaining_clients": remainingClients,
			"reason":            disconnectReason,
		}).Info("Client disconnected")

		// Call disconnect callback if set
		if s.OnClientDisconnect != nil {
			s.OnClientDisconnect(clientCN, disconnectReason)
		}

		stream.Close()
	}()

	s.wg.Add(1)
	go s.startHeartbeat(stream, clientCN)

	buf := make([]byte, 4096)
	for {
		// Check context cancellation
		select {
		case <-s.ctx.Done():
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "shutdown",
				"client_cn": clientCN,
			}).Info("Closing connection due to server shutdown")
			return
		default:
		}

		// Set read deadline to allow periodic context checks
		stream.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := stream.Read(buf)
		if err != nil {
			// Check if error is due to timeout (expected for periodic checks)
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				continue
			}
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "stream_read",
				"client_cn": clientCN,
				"error":     err,
			}).Error("Stream read error, closing connection")
			// Mark state and close connection
			s.clientInfoMu.Lock()
			if info := s.clientInfo[stream]; info != nil {
				info.State = "read_error"
			}
			s.clientInfoMu.Unlock()
			stream.Close()
			return
		}

		if n == 0 {
			continue
		}

		frameType, payload := ParseFrame(buf[:n])

		s.metrics.IncrementFramesReceived(frameType, n)

		switch frameType {
		case FrameTypePing:
			// Respond to ping with pong
			pongFrame := CreatePongFrame()
			if _, err := stream.Write(pongFrame); err != nil {
				logrus.WithFields(logrus.Fields{
					"component": "server",
					"action":    "send_pong",
					"client_cn": clientCN,
					"error":     err,
				}).Error("Failed to send pong response")
				s.metrics.RecordError("server", "send_pong", err.Error(), clientCN, "error")
				// Mark state and close connection
				s.clientInfoMu.Lock()
				if info := s.clientInfo[stream]; info != nil {
					info.State = "write_error"
				}
				s.clientInfoMu.Unlock()
				stream.Close()
				return
			}
			s.metrics.IncrementFramesSent(FrameTypePong, len(pongFrame))
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "ping_pong",
				"client_cn": clientCN,
			}).Info("Responded to ping with pong")
		case FrameTypePong:
			// Received pong, connection is alive - update LastPong time
			s.clientInfoMu.Lock()
			if info := s.clientInfo[stream]; info != nil {
				info.LastPong = time.Now()
			}
			s.clientInfoMu.Unlock()
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "heartbeat",
				"client_cn": clientCN,
			}).Info("Received pong from client")
		case FrameTypeData:
			// Handle data frame
			if s.Handler != nil && len(payload) > 0 {
				logrus.WithFields(logrus.Fields{
					"component":   "handler",
					"action":      "data_received",
					"data_size":   len(payload),
					"data":        string(payload),
					"client_cn":   clientCN,
					"cert_serial": clientSerial,
				}).Info("Received data from client")
				s.Handler(payload, stream)
			}
		case FrameTypeSubscribe, FrameTypeUnsubscribe, FrameTypePublish:
			// Handle pub/sub frames
			frameType, topic, data := ParseTopicFrame(buf[:n])
			if topic == "" {
				logrus.WithFields(logrus.Fields{
					"component": "server",
					"action":    "invalid_topic_frame",
					"client_cn": clientCN,
				}).Warn("Received invalid topic frame")
				break
			}
			s.handlePubSubFrame(frameType, topic, data, stream, clientCN)
		case FrameTypeEmitEvent, FrameTypeOnEvent, FrameTypeOffEvent:
			// Handle event frames
			frameType, eventName, data := ParseEventFrame(buf[:n])
			if eventName == "" {
				logrus.WithFields(logrus.Fields{
					"component": "server",
					"action":    "invalid_event_frame",
					"client_cn": clientCN,
				}).Warn("Received invalid event frame")
				break
			}
			s.handleEventFrame(frameType, eventName, data, stream, clientCN)
		default:
			logrus.WithFields(logrus.Fields{
				"component":  "server",
				"action":     "unknown_frame",
				"client_cn":  clientCN,
				"frame_type": fmt.Sprintf("0x%02x", frameType),
			}).Warn("Received unknown frame type from client")
		}
	}
}

func (s *Server) startHeartbeat(stream *Stream, clientCN string) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "shutdown",
				"client_cn": clientCN,
			}).Debug("Stopping heartbeat due to server shutdown")
			return
		case <-ticker.C:
			// Check if client hasn't responded to pings (timeout detection)
			s.clientInfoMu.RLock()
			info := s.clientInfo[stream]
			s.clientInfoMu.RUnlock()

			if info != nil {
				timeSinceLastPong := time.Since(info.LastPong)
				if timeSinceLastPong > s.heartbeatInterval*3 {
					logrus.WithFields(logrus.Fields{
						"component":            "server",
						"action":               "heartbeat_timeout",
						"client_cn":            clientCN,
						"time_since_last_pong": timeSinceLastPong.String(),
					}).Warn("Client heartbeat timeout detected, closing connection")
					s.clientInfoMu.Lock()
					if info := s.clientInfo[stream]; info != nil {
						info.State = "heartbeat_timeout"
					}
					s.clientInfoMu.Unlock()
					// Close connection on timeout
					stream.Close()
					return
				}
			}

			pingFrame := CreatePingFrame()
			if _, err := stream.Write(pingFrame); err != nil {
				logrus.WithFields(logrus.Fields{
					"component": "server",
					"action":    "heartbeat",
					"client_cn": clientCN,
					"error":     err,
				}).Error("Heartbeat ping failed, closing connection")
				s.metrics.RecordError("server", "heartbeat", err.Error(), clientCN, "error")
				// Mark as write_error and close connection
				s.clientInfoMu.Lock()
				if info := s.clientInfo[stream]; info != nil {
					info.State = "write_error"
				}
				s.clientInfoMu.Unlock()
				stream.Close()
				return
			}
			s.metrics.IncrementFramesSent(FrameTypePing, len(pingFrame))
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "heartbeat",
				"client_cn": clientCN,
			}).Info("Sent heartbeat ping to client")
		}
	}
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(timeout time.Duration) error {
	logrus.WithFields(logrus.Fields{
		"component": "server",
		"action":    "shutdown",
		"timeout":   timeout.String(),
	}).Info("Received shutdown signal, initiating graceful shutdown")

	// Cancel context to signal all goroutines to stop
	s.cancel()

	// Close listener to stop accepting new connections
	s.listenerMu.Lock()
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "shutdown",
				"error":     err,
			}).Warn("Error closing listener")
		}
		s.listener = nil
	}
	s.listenerMu.Unlock()

	// Close all active client connections
	s.clientsMu.Lock()
	clients := make([]*Stream, 0, len(s.Clients))
	for stream := range s.Clients {
		clients = append(clients, stream)
	}
	s.clientsMu.Unlock()

	logrus.WithFields(logrus.Fields{
		"component":      "server",
		"action":         "shutdown",
		"active_clients": len(clients),
	}).Info("Closing active client connections")

	for _, stream := range clients {
		if err := stream.Close(); err != nil {
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "shutdown",
				"error":     err,
			}).Warn("Error closing client stream")
		}
	}

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logrus.WithFields(logrus.Fields{
			"component": "server",
			"action":    "shutdown",
		}).Info("All goroutines finished, server shutdown complete")
		return nil
	case <-time.After(timeout):
		logrus.WithFields(logrus.Fields{
			"component": "server",
			"action":    "shutdown",
			"timeout":   timeout.String(),
		}).Error("Shutdown timeout exceeded, some goroutines may still be running")
		return fmt.Errorf("shutdown timeout exceeded")
	}
}

// HandleError is a public method to log errors from outside the package
func (s *Server) HandleError(component, action, errMsg, clientCN, severity string) {
	s.metrics.RecordError(component, action, errMsg, clientCN, severity)

	fields := logrus.Fields{
		"component": component,
		"action":    action,
		"error":     errMsg,
	}
	if clientCN != "" {
		fields["client_cn"] = clientCN
	}

	switch severity {
	case "fatal":
		logrus.WithFields(fields).Fatal("Fatal error occurred")
	case "error":
		logrus.WithFields(fields).Error("Error occurred")
	case "warn":
		logrus.WithFields(fields).Warn("Warning")
	default:
		logrus.WithFields(fields).Error("Error occurred")
	}
}

// handlePubSubFrame handles pub/sub frames (Subscribe, Unsubscribe, Publish)
func (s *Server) handlePubSubFrame(frameType byte, topic string, data []byte, stream *Stream, clientCN string) {
	switch frameType {
	case FrameTypeSubscribe:
		s.topicsMu.Lock()
		if s.topics[topic] == nil {
			s.topics[topic] = make(map[*Stream]struct{})
		}
		s.topics[topic][stream] = struct{}{}
		subscriberCount := len(s.topics[topic])
		s.topicsMu.Unlock()

		logrus.WithFields(logrus.Fields{
			"component":        "server",
			"action":           "subscribe",
			"client_cn":        clientCN,
			"topic":            topic,
			"subscriber_count": subscriberCount,
		}).Info("Client subscribed to topic")

	case FrameTypeUnsubscribe:
		s.topicsMu.Lock()
		if subscribers, exists := s.topics[topic]; exists {
			delete(subscribers, stream)
			if len(subscribers) == 0 {
				delete(s.topics, topic)
			}
		}
		s.topicsMu.Unlock()

		logrus.WithFields(logrus.Fields{
			"component": "server",
			"action":    "unsubscribe",
			"client_cn": clientCN,
			"topic":     topic,
		}).Info("Client unsubscribed from topic")

	case FrameTypePublish:
		// Send message to all subscribers of the topic
		s.topicsMu.RLock()
		subscribers := make([]*Stream, 0, len(s.topics[topic]))
		for sub := range s.topics[topic] {
			subscribers = append(subscribers, sub)
		}
		s.topicsMu.RUnlock()

		publishFrame := CreateTopicFrame(FrameTypePublish, topic, data)
		sentCount := 0
		for _, sub := range subscribers {
			if _, err := sub.Write(publishFrame); err != nil {
				logrus.WithFields(logrus.Fields{
					"component": "server",
					"action":    "publish",
					"topic":     topic,
					"error":     err,
				}).Warn("Failed to send publish message to subscriber")
				continue
			}
			sentCount++
		}

		logrus.WithFields(logrus.Fields{
			"component":  "server",
			"action":     "publish",
			"client_cn":  clientCN,
			"topic":      topic,
			"sent_count": sentCount,
			"total_subs": len(subscribers),
		}).Info("Published message to topic subscribers")
	}
}

// handleEventFrame handles event frames (EmitEvent, OnEvent, OffEvent)
func (s *Server) handleEventFrame(frameType byte, eventName string, data []byte, stream *Stream, clientCN string) {
	switch frameType {
	case FrameTypeOnEvent:
		s.eventListenersMu.Lock()
		if s.eventListeners[eventName] == nil {
			s.eventListeners[eventName] = make(map[*Stream]struct{})
		}
		s.eventListeners[eventName][stream] = struct{}{}
		listenerCount := len(s.eventListeners[eventName])
		s.eventListenersMu.Unlock()

		logrus.WithFields(logrus.Fields{
			"component":      "server",
			"action":         "on_event",
			"client_cn":      clientCN,
			"event_name":     eventName,
			"listener_count": listenerCount,
		}).Info("Client listening to event")

	case FrameTypeOffEvent:
		s.eventListenersMu.Lock()
		if listeners, exists := s.eventListeners[eventName]; exists {
			delete(listeners, stream)
			if len(listeners) == 0 {
				delete(s.eventListeners, eventName)
			}
		}
		s.eventListenersMu.Unlock()

		logrus.WithFields(logrus.Fields{
			"component":  "server",
			"action":     "off_event",
			"client_cn":  clientCN,
			"event_name": eventName,
		}).Info("Client stopped listening to event")

	case FrameTypeEmitEvent:
		// Send event to all listeners
		s.eventListenersMu.RLock()
		listeners := make([]*Stream, 0, len(s.eventListeners[eventName]))
		for listener := range s.eventListeners[eventName] {
			listeners = append(listeners, listener)
		}
		s.eventListenersMu.RUnlock()

		eventFrame := CreateEventFrame(FrameTypeEmitEvent, eventName, data)
		sentCount := 0
		for _, listener := range listeners {
			if _, err := listener.Write(eventFrame); err != nil {
				logrus.WithFields(logrus.Fields{
					"component":  "server",
					"action":     "emit_event",
					"event_name": eventName,
					"error":      err,
				}).Warn("Failed to send event to listener")
				continue
			}
			sentCount++
		}

		logrus.WithFields(logrus.Fields{
			"component":       "server",
			"action":          "emit_event",
			"client_cn":       clientCN,
			"event_name":      eventName,
			"sent_count":      sentCount,
			"total_listeners": len(listeners),
		}).Info("Emitted event to listeners")
	}
}

// startMetricsLogger periodically logs metrics to a file
func (s *Server) startMetricsLogger() {
	defer s.wg.Done()

	// Log metrics every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			logrus.WithFields(logrus.Fields{
				"component": "server",
				"action":    "shutdown",
			}).Debug("Stopping metrics logger due to server shutdown")
			return
		case <-ticker.C:
			// Get metrics snapshot
			snapshot := s.metrics.GetSnapshot()

			// Format metrics as a log entry
			logEntry := fmt.Sprintf(
				"[%s] Metrics - Uptime: %s | Active Connections: %d | Total Connections: %d | Rejected: %d | "+
					"Frames Received: %d | Frames Sent: %d | Bytes Received: %d | Bytes Sent: %d | "+
					"Errors Total: %d | Last Error: %s\n",
				time.Now().Format(time.RFC3339),
				snapshot.Uptime.String(),
				snapshot.ActiveConnections,
				snapshot.TotalConnections,
				snapshot.RejectedConnections,
				snapshot.FramesReceived,
				snapshot.FramesSent,
				snapshot.BytesReceived,
				snapshot.BytesSent,
				snapshot.ErrorsTotal,
				formatTime(snapshot.LastError),
			)

			// Append to metrics log file
			file, err := os.OpenFile(s.metricsLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				logrus.WithFields(logrus.Fields{
					"component": "server",
					"action":    "metrics_log",
					"error":     err,
					"path":      s.metricsLogPath,
				}).Error("Failed to open metrics log file")
				continue
			}

			if _, err := file.WriteString(logEntry); err != nil {
				logrus.WithFields(logrus.Fields{
					"component": "server",
					"action":    "metrics_log",
					"error":     err,
				}).Error("Failed to write metrics to log file")
			}

			file.Close()
		}
	}
}

// formatTime formats a time value for logging
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format(time.RFC3339)
}

func (s *Server) getTLSConfig() (*tls.Config, error) {
	config, err := certs.GetTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get TLS config: %w", err)
	}
	return config, nil
}
