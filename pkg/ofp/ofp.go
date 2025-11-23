package ofp

import (
	"context"
	"crypto/tls"

	quicgo "github.com/quic-go/quic-go"
	"github.com/sirupsen/logrus"
)

func init() {
	// Configure logger
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
	logrus.SetLevel(logrus.InfoLevel)
}

type Stream = quicgo.Stream
type Conn = quicgo.Conn
type Listener = *quicgo.Listener
type Config = quicgo.Config

func ListenAddr(addr string, tlsConfig *tls.Config, config *Config) (Listener, error) {
	return quicgo.ListenAddr(addr, tlsConfig, config)
}

func DialAddr(addr string, tlsConfig *tls.Config, config *Config) (*Conn, error) {
	return quicgo.DialAddr(context.Background(), addr, tlsConfig, config)
}
