package main

import (
	"log"

	"github.com/devprism0dev/Open-Frame-Protokol/pkg/ofp"
)

func main() {
	// Data handler function (existing functionality)
	handler := func(data []byte, stream *ofp.Stream) {
		ofp.WriteDataFrame(stream, data)
	}

	// Client disconnect callback (new feature)
	onDisconnect := func(clientCN string, reason string) {
		log.Printf("Client %s disconnected. Reason: %s", clientCN, reason)
	}

	server := ofp.NewServer(ofp.ServerConfig{
		Addr:               "0.0.0.0:4433",
		Handler:            handler,
		ShowBanner:         true,
		OnClientDisconnect: onDisconnect,
	})

	// Server now supports:
	// - Traditional data frames (existing)
	// - Pub/Sub topics (new)
	// - Event-based communication (new)
	// - Client disconnect tracking (new)

	if err := server.Listen(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
