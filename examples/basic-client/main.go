package main

import (
	"fmt"
	"log"
	"time"

	"github.com/devprism0dev/Open-Frame-Protokol/pkg/ofp"
)

func main() {
	// Traditional data handler (existing functionality)
	handler := func(data []byte, stream *ofp.Stream) {
		log.Printf("Received data: %s", string(data))
	}

	client := ofp.NewClient(ofp.ClientConfig{
		Addr:    "localhost:4433",
		Handler: handler,
	})

	if err := client.Connect("test-client"); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	log.Println("Connected successfully!")

	// Example 1: Subscribe to a topic
	err := client.Subscribe("news", func(data []byte) {
		log.Printf("Received news: %s", string(data))
	})
	if err != nil {
		log.Printf("Failed to subscribe: %v", err)
	}

	// Example 2: Listen to an event
	err = client.On("user-login", func(data []byte) {
		log.Printf("User logged in: %s", string(data))
	})
	if err != nil {
		log.Printf("Failed to register event listener: %v", err)
	}

	// Example 3: Publish to a topic
	for i := 0; i < 3; i++ {
		message := []byte(fmt.Sprintf("News item #%d", i+1))
		if err := client.Publish("news", message); err != nil {
			log.Printf("Failed to publish: %v", err)
		}
		time.Sleep(1 * time.Second)
	}

	// Example 4: Emit an event
	if err := client.Emit("user-login", []byte("john.doe")); err != nil {
		log.Printf("Failed to emit event: %v", err)
	}

	// Example 5: Traditional data sending (existing)
	for i := 0; i < 3; i++ {
		message := []byte(fmt.Sprintf("Hello from client #%d", i+1))
		if err := client.SendData(message); err != nil {
			log.Printf("Failed to send data: %v", err)
		}
		time.Sleep(2 * time.Second)
	}

	log.Println("Examples completed. Press Ctrl+C to exit.")

	// Keep connection alive
	select {}
}
