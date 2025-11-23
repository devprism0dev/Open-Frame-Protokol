# OFP (Open Frame Protocol)

<div align="center">
  <img src="docs/logo/logo.png" alt="OFP Logo" width="400">
</div>

A high-performance, secure communication protocol library built on QUIC for Go applications.

## Features

- **QUIC-based**: Built on top of QUIC for better performance and connection multiplexing
- **TLS Security**: Full TLS encryption with certificate-based authentication
- **Frame-based Protocol**: Efficient binary frame format for data transmission
- **Pub/Sub Support**: Built-in publish/subscribe messaging
- **Event System**: Event-driven communication patterns
- **Heartbeat**: Automatic connection health monitoring
- **Metrics**: Built-in metrics collection and logging

## Installation

```bash
go get github.com/devprism0dev/Open-Frame-Protokol
```


## Quick Start

### Server Example

```go
package main

import (
    "github.com/devprism0dev/Open-Frame-Protokol/pkg/ofp"
    "log"
)

func main() {
    handler := func(data []byte, stream *ofp.Stream) {
        ofp.WriteDataFrame(stream, data)
    }

    server := ofp.NewServer(ofp.ServerConfig{
        Addr:    "0.0.0.0:4433",
        Handler: handler,
        ShowBanner: true,
    })

    if err := server.Listen(); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }
}
```

### Client Example

```go
package main

import (
    "github.com/devprism0dev/Open-Frame-Protokol/pkg/ofp"
    "log"
)

func main() {
    handler := func(data []byte, stream *ofp.Stream) {
        log.Printf("Received: %s", string(data))
    }

    client := ofp.NewClient(ofp.ClientConfig{
        Addr:    "localhost:4433",
        Handler: handler,
    })

    if err := client.Connect("my-client"); err != nil {
        log.Fatalf("Failed to connect: %v", err)
    }
    defer client.Close()

    // Send data
    client.SendData([]byte("Hello, Server!"))
}
```

## Features in Detail

### Pub/Sub Messaging

```go
// Subscribe to a topic
client.Subscribe("news", func(data []byte) {
    log.Printf("News: %s", string(data))
})

// Publish to a topic
client.Publish("news", []byte("Breaking news!"))
```

### Event System

```go
// Listen to events
client.On("user-login", func(data []byte) {
    log.Printf("User logged in: %s", string(data))
})

// Emit events
client.Emit("user-login", []byte("john.doe"))
```

## Certificate Management

OFP automatically generates and manages TLS certificates. Certificates are stored in a `certs/` directory relative to your application.

- Server certificates are automatically created on first run
- Client certificates are generated per client connection
- All certificates are signed by a common CA

## Examples

See the `examples/` directory for more complete examples:

- `basic-server/` - Simple server implementation
- `basic-client/` - Simple client implementation

## Requirements

- Go 1.25.1 or later
- QUIC support (provided by `github.com/quic-go/quic-go`)

## License

See LICENSE file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

