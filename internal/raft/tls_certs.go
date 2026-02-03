package raft

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// CertOptions holds options for certificate generation
type CertOptions struct {
	// Common options
	Organization string
	Country      string
	Province     string
	Locality     string
	ValidFor     time.Duration

	// CA options
	IsCA bool

	// Server/Client options
	Hosts      []string // DNS names and IP addresses
	CommonName string
}

// DefaultCertOptions returns default certificate options
func DefaultCertOptions() *CertOptions {
	return &CertOptions{
		Organization: "Helios Raft Cluster",
		Country:      "US",
		ValidFor:     365 * 24 * time.Hour, // 1 year
		Hosts:        []string{"localhost", "127.0.0.1"},
	}
}

// GenerateCA generates a Certificate Authority
func GenerateCA(opts *CertOptions) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if opts == nil {
		opts = DefaultCertOptions()
	}
	opts.IsCA = true
	opts.CommonName = "Helios Raft CA"

	// Generate private key
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Generate serial number
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Create CA certificate template
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{opts.Organization},
			Country:      []string{opts.Country},
			Province:     []string{opts.Province},
			Locality:     []string{opts.Locality},
			CommonName:   opts.CommonName,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(opts.ValidFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Self-sign the CA certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, priv, nil
}

// GenerateCertificate generates a certificate signed by a CA
func GenerateCertificate(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, opts *CertOptions) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if opts == nil {
		opts = DefaultCertOptions()
	}

	// Generate private key
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Generate serial number
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{opts.Organization},
			Country:      []string{opts.Country},
			Province:     []string{opts.Province},
			Locality:     []string{opts.Locality},
			CommonName:   opts.CommonName,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(opts.ValidFor),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	// Add hosts (DNS names and IP addresses)
	for _, h := range opts.Hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	// Sign the certificate with the CA
	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, priv, nil
}

// SaveCertificate saves a certificate to a PEM file
func SaveCertificate(cert *x509.Certificate, filename string) error {
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})

	return os.WriteFile(filename, certPEM, 0644)
}

// SavePrivateKey saves a private key to a PEM file
func SavePrivateKey(key *ecdsa.PrivateKey, filename string) error {
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	})

	return os.WriteFile(filename, keyPEM, 0600)
}

// GenerateClusterCertificates generates certificates for a Raft cluster
func GenerateClusterCertificates(outputDir string, nodeIDs []string, hosts map[string][]string) error {
	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Generate CA
	caOpts := DefaultCertOptions()
	caCert, caKey, err := GenerateCA(caOpts)
	if err != nil {
		return fmt.Errorf("failed to generate CA: %w", err)
	}

	// Save CA certificate
	if err := SaveCertificate(caCert, fmt.Sprintf("%s/ca.crt", outputDir)); err != nil {
		return fmt.Errorf("failed to save CA certificate: %w", err)
	}

	// Save CA private key
	if err := SavePrivateKey(caKey, fmt.Sprintf("%s/ca.key", outputDir)); err != nil {
		return fmt.Errorf("failed to save CA private key: %w", err)
	}

	// Generate certificates for each node
	for _, nodeID := range nodeIDs {
		certOpts := DefaultCertOptions()
		certOpts.CommonName = nodeID

		// Add node-specific hosts
		if nodeHosts, ok := hosts[nodeID]; ok {
			certOpts.Hosts = append(certOpts.Hosts, nodeHosts...)
		}

		nodeCert, nodeKey, err := GenerateCertificate(caCert, caKey, certOpts)
		if err != nil {
			return fmt.Errorf("failed to generate certificate for node %s: %w", nodeID, err)
		}

		// Save node certificate
		certFile := fmt.Sprintf("%s/%s.crt", outputDir, nodeID)
		if err := SaveCertificate(nodeCert, certFile); err != nil {
			return fmt.Errorf("failed to save certificate for node %s: %w", nodeID, err)
		}

		// Save node private key
		keyFile := fmt.Sprintf("%s/%s.key", outputDir, nodeID)
		if err := SavePrivateKey(nodeKey, keyFile); err != nil {
			return fmt.Errorf("failed to save private key for node %s: %w", nodeID, err)
		}
	}

	return nil
}
