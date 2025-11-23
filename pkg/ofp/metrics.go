package ofp

import (
	"sync"
	"time"
)

// Metrics holds server metrics
type Metrics struct {
	mu sync.RWMutex

	// Connection metrics
	TotalConnections        uint64
	ActiveConnections       uint64
	RejectedConnections     uint64
	DisconnectedConnections uint64

	// Frame metrics
	FramesReceived     uint64
	FramesSent         uint64
	DataFramesReceived uint64
	DataFramesSent     uint64
	PingFramesReceived uint64
	PingFramesSent     uint64
	PongFramesReceived uint64
	PongFramesSent     uint64
	UnknownFrames      uint64

	// Error metrics
	ErrorsTotal       uint64
	AcceptErrors      uint64
	StreamReadErrors  uint64
	StreamWriteErrors uint64
	HeartbeatErrors   uint64
	HandlerErrors     uint64

	// Data metrics
	BytesReceived uint64
	BytesSent     uint64

	// Timing metrics
	Uptime               time.Duration
	StartTime            time.Time
	LastClientConnect    time.Time
	LastClientDisconnect time.Time
	LastError            time.Time

	// Recent errors (last 100)
	RecentErrors    []ErrorLog
	maxRecentErrors int
}

// ErrorLog represents an error entry
type ErrorLog struct {
	Timestamp time.Time
	Component string
	Action    string
	Error     string
	ClientCN  string
	Severity  string // "error", "warn", "fatal"
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		StartTime:       time.Now(),
		RecentErrors:    make([]ErrorLog, 0, 100),
		maxRecentErrors: 100,
	}
}

// IncrementTotalConnections increments total connections counter
func (m *Metrics) IncrementTotalConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalConnections++
	m.ActiveConnections++
	m.LastClientConnect = time.Now()
	m.updateUptime()
}

// IncrementRejectedConnections increments rejected connections counter
func (m *Metrics) IncrementRejectedConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RejectedConnections++
	m.updateUptime()
}

// DecrementActiveConnections decrements active connections counter
func (m *Metrics) DecrementActiveConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ActiveConnections > 0 {
		m.ActiveConnections--
	}
	m.DisconnectedConnections++
	m.LastClientDisconnect = time.Now()
	m.updateUptime()
}

// IncrementFramesReceived increments frames received counter
func (m *Metrics) IncrementFramesReceived(frameType byte, size int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FramesReceived++
	m.BytesReceived += uint64(size)

	switch frameType {
	case FrameTypeData:
		m.DataFramesReceived++
	case FrameTypePing:
		m.PingFramesReceived++
	case FrameTypePong:
		m.PongFramesReceived++
	default:
		m.UnknownFrames++
	}
	m.updateUptime()
}

// IncrementFramesSent increments frames sent counter
func (m *Metrics) IncrementFramesSent(frameType byte, size int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FramesSent++
	m.BytesSent += uint64(size)

	switch frameType {
	case FrameTypeData:
		m.DataFramesSent++
	case FrameTypePing:
		m.PingFramesSent++
	case FrameTypePong:
		m.PongFramesSent++
	}
	m.updateUptime()
}

// RecordError records an error
func (m *Metrics) RecordError(component, action, errMsg, clientCN, severity string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ErrorsTotal++
	m.LastError = time.Now()

	switch action {
	case "accept":
		m.AcceptErrors++
	case "stream_read":
		m.StreamReadErrors++
	case "stream_write", "send_pong", "send_response":
		m.StreamWriteErrors++
	case "heartbeat":
		m.HeartbeatErrors++
	case "handler":
		m.HandlerErrors++
	}

	// Add to recent errors
	errorLog := ErrorLog{
		Timestamp: time.Now(),
		Component: component,
		Action:    action,
		Error:     errMsg,
		ClientCN:  clientCN,
		Severity:  severity,
	}

	m.RecentErrors = append(m.RecentErrors, errorLog)
	if len(m.RecentErrors) > m.maxRecentErrors {
		m.RecentErrors = m.RecentErrors[1:]
	}

	m.updateUptime()
}

// GetSnapshot returns a snapshot of current metrics
func (m *Metrics) GetSnapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.updateUptime()

	// Copy recent errors
	errors := make([]ErrorLog, len(m.RecentErrors))
	copy(errors, m.RecentErrors)

	return MetricsSnapshot{
		TotalConnections:        m.TotalConnections,
		ActiveConnections:       m.ActiveConnections,
		RejectedConnections:     m.RejectedConnections,
		DisconnectedConnections: m.DisconnectedConnections,
		FramesReceived:          m.FramesReceived,
		FramesSent:              m.FramesSent,
		DataFramesReceived:      m.DataFramesReceived,
		DataFramesSent:          m.DataFramesSent,
		PingFramesReceived:      m.PingFramesReceived,
		PingFramesSent:          m.PingFramesSent,
		PongFramesReceived:      m.PongFramesReceived,
		PongFramesSent:          m.PongFramesSent,
		UnknownFrames:           m.UnknownFrames,
		ErrorsTotal:             m.ErrorsTotal,
		AcceptErrors:            m.AcceptErrors,
		StreamReadErrors:        m.StreamReadErrors,
		StreamWriteErrors:       m.StreamWriteErrors,
		HeartbeatErrors:         m.HeartbeatErrors,
		HandlerErrors:           m.HandlerErrors,
		BytesReceived:           m.BytesReceived,
		BytesSent:               m.BytesSent,
		Uptime:                  m.Uptime,
		StartTime:               m.StartTime,
		LastClientConnect:       m.LastClientConnect,
		LastClientDisconnect:    m.LastClientDisconnect,
		LastError:               m.LastError,
		RecentErrors:            errors,
	}
}

// MetricsSnapshot is a thread-safe snapshot of metrics
type MetricsSnapshot struct {
	TotalConnections        uint64
	ActiveConnections       uint64
	RejectedConnections     uint64
	DisconnectedConnections uint64
	FramesReceived          uint64
	FramesSent              uint64
	DataFramesReceived      uint64
	DataFramesSent          uint64
	PingFramesReceived      uint64
	PingFramesSent          uint64
	PongFramesReceived      uint64
	PongFramesSent          uint64
	UnknownFrames           uint64
	ErrorsTotal             uint64
	AcceptErrors            uint64
	StreamReadErrors        uint64
	StreamWriteErrors       uint64
	HeartbeatErrors         uint64
	HandlerErrors           uint64
	BytesReceived           uint64
	BytesSent               uint64
	Uptime                  time.Duration
	StartTime               time.Time
	LastClientConnect       time.Time
	LastClientDisconnect    time.Time
	LastError               time.Time
	RecentErrors            []ErrorLog
}

func (m *Metrics) updateUptime() {
	m.Uptime = time.Since(m.StartTime)
}
