package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	CertDir      = "certs"
	CertFile     = "server.crt"
	KeyFile      = "server.key"
	CAFile       = "ca.crt"
	CAKeyFile    = "ca.key"
	CertValidity = 365 * 24 * time.Hour
)

// getCertDir returns the absolute path to the certs directory
// It searches for the certs directory starting from the current working directory
// and walking up to find go.mod (project root)
func getCertDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	// Start from current directory and walk up to find go.mod
	dir := wd
	for {
		certPath := filepath.Join(dir, CertDir)
		if _, err := os.Stat(certPath); err == nil {
			// Found certs directory
			return certPath, nil
		}

		// Check if we're at project root (has go.mod)
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// We're at project root, use certs directory here
			return filepath.Join(dir, CertDir), nil
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root, use current directory's certs
			break
		}
		dir = parent
	}

	// Fallback: use current working directory
	return filepath.Join(wd, CertDir), nil
}

func GetOrCreateCertificates() (*tls.Certificate, error) {
	certDir, err := getCertDir()
	if err != nil {
		return nil, err
	}
	certPath := filepath.Join(certDir, CertFile)
	keyPath := filepath.Join(certDir, KeyFile)

	if certExists(certPath) && certExists(keyPath) {
		log.Println("Certificates found, loading...")
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			log.Printf("Certificate loading error: %v, recreating...", err)
			return createAndSaveCertificates(certPath, keyPath)
		}
		log.Println("Certificates loaded successfully")
		return &cert, nil
	}

	log.Println("Certificates not found, creating...")
	return createAndSaveCertificates(certPath, keyPath)
}

func createAndSaveCertificates(certPath, keyPath string) (*tls.Certificate, error) {
	certDir := filepath.Dir(certPath)
	if err := os.MkdirAll(certDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create certificate directory: %w", err)
	}

	// RSA anahtarı oluştur
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	caCert, caKey, err := GetOrCreateCA()
	if err != nil {
		return nil, fmt.Errorf("failed to get CA: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:       []string{"OpenFrameProtocol"},
			Country:            []string{"TR"},
			Province:           []string{""},
			Locality:           []string{""},
			StreetAddress:      []string{""},
			PostalCode:         []string{""},
			CommonName:         "localhost",
			OrganizationalUnit: []string{"OFP"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(CertValidity),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate file: %w", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	}); err != nil {
		return nil, fmt.Errorf("failed to write certificate: %w", err)
	}

	keyFile, err := os.Create(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create key file: %w", err)
	}
	defer keyFile.Close()

	privateKeyDER := x509.MarshalPKCS1PrivateKey(privateKey)
	if err := pem.Encode(keyFile, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyDER,
	}); err != nil {
		return nil, fmt.Errorf("failed to write key: %w", err)
	}

	log.Printf("Certificates created successfully: %s and %s", certPath, keyPath)

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load created certificate: %w", err)
	}

	return &cert, nil
}

func certExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func GetOrCreateCA() (*x509.Certificate, *rsa.PrivateKey, error) {
	certDir, err := getCertDir()
	if err != nil {
		return nil, nil, err
	}
	caCertPath := filepath.Join(certDir, CAFile)
	caKeyPath := filepath.Join(certDir, CAKeyFile)

	if certExists(caCertPath) && certExists(caKeyPath) {
		log.Println("CA certificate found, loading...")
		caCertPEM, err := os.ReadFile(caCertPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		caKeyPEM, err := os.ReadFile(caKeyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA key: %w", err)
		}

		block, _ := pem.Decode(caCertPEM)
		caCert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
		}

		keyBlock, _ := pem.Decode(caKeyPEM)
		caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse CA key: %w", err)
		}

		log.Println("CA certificate loaded successfully")
		return caCert, caKey, nil
	}

	log.Println("CA certificate not found, creating...")
	return createCA(caCertPath, caKeyPath)
}

func createCA(caCertPath, caKeyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certDir := filepath.Dir(caCertPath)
	if err := os.MkdirAll(certDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate directory: %w", err)
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:       []string{"OpenFrameProtocol CA"},
			Country:            []string{"TR"},
			Province:           []string{""},
			Locality:           []string{""},
			StreetAddress:      []string{""},
			PostalCode:         []string{""},
			CommonName:         "OFP Certificate Authority",
			OrganizationalUnit: []string{"OFP"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(CertValidity * 10),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCertFile, err := os.Create(caCertPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CA certificate file: %w", err)
	}
	defer caCertFile.Close()

	if err := pem.Encode(caCertFile, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caDER,
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to write CA certificate: %w", err)
	}

	caKeyFile, err := os.Create(caKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create CA key file: %w", err)
	}
	defer caKeyFile.Close()

	if err := pem.Encode(caKeyFile, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caKey),
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to write CA key: %w", err)
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse created CA certificate: %w", err)
	}

	log.Printf("CA certificate created successfully: %s and %s", caCertPath, caKeyPath)
	return caCert, caKey, nil
}

func GetTLSConfig() (*tls.Config, error) {
	cert, err := GetOrCreateCertificates()
	if err != nil {
		return nil, err
	}

	certDir, err := getCertDir()
	if err != nil {
		return nil, err
	}
	caCertPath := filepath.Join(certDir, CAFile)
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	block, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(caCert)

	return &tls.Config{
		Certificates: []tls.Certificate{*cert},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{"ofp-protocol"},
	}, nil
}

func GetClientTLSConfig(clientName string) (*tls.Config, error) {
	caCertPool := x509.NewCertPool()

	// Get cert directory
	certDir, err := getCertDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get cert directory: %w", err)
	}

	// Load server's CA certificate (this is the root authority that signed server's cert)
	caCertPath := filepath.Join(certDir, CAFile)
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	// Add CA certificate to pool using AppendCertsFromPEM (preferred method)
	if ok := caCertPool.AppendCertsFromPEM(caCertPEM); !ok {
		// Fallback: parse and add manually
		for len(caCertPEM) > 0 {
			var block *pem.Block
			block, caCertPEM = pem.Decode(caCertPEM)
			if block == nil {
				break
			}
			if block.Type == "CERTIFICATE" {
				caCert, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
				}
				caCertPool.AddCert(caCert)
			}
		}
	}

	// Also add server's certificate to the pool as a trusted certificate
	// This allows client to verify server's certificate directly
	serverCertPath := filepath.Join(certDir, CertFile)
	serverCertPEM, err := os.ReadFile(serverCertPath)
	if err == nil {
		// Try to add server certificate to pool
		if ok := caCertPool.AppendCertsFromPEM(serverCertPEM); !ok {
			// Fallback: parse and add manually
			for len(serverCertPEM) > 0 {
				var block *pem.Block
				block, serverCertPEM = pem.Decode(serverCertPEM)
				if block == nil {
					break
				}
				if block.Type == "CERTIFICATE" {
					serverCert, err := x509.ParseCertificate(block.Bytes)
					if err == nil {
						caCertPool.AddCert(serverCert)
					}
				}
			}
		}
	}

	// Create client certificate signed by the same CA
	certPEM, keyPEM, err := CreateClientCertificate(clientName)
	if err != nil {
		return nil, fmt.Errorf("failed to create client certificate: %w", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caCertPool,
		InsecureSkipVerify: false,
		NextProtos:         []string{"ofp-protocol"},
	}, nil
}

func CreateClientCertificate(clientName string) ([]byte, []byte, error) {
	caCert, caKey, err := GetOrCreateCA()
	if err != nil {
		return nil, nil, err
	}

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate client key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			Organization:       []string{"OpenFrameProtocol"},
			Country:            []string{"TR"},
			Province:           []string{""},
			Locality:           []string{""},
			StreetAddress:      []string{""},
			PostalCode:         []string{""},
			CommonName:         clientName,
			OrganizationalUnit: []string{"OFP Client"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(CertValidity),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
	})

	return certPEM, keyPEM, nil
}
