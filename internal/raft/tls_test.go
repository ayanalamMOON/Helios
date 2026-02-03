package raft

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateCertificates(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Test CA generation
	t.Run("GenerateCA", func(t *testing.T) {
		opts := DefaultCertOptions()
		caCert, caKey, err := GenerateCA(opts)
		if err != nil {
			t.Fatalf("Failed to generate CA: %v", err)
		}

		if caCert == nil {
			t.Fatal("CA certificate is nil")
		}
		if caKey == nil {
			t.Fatal("CA private key is nil")
		}

		if !caCert.IsCA {
			t.Error("Certificate is not marked as CA")
		}
	})

	// Test certificate generation
	t.Run("GenerateCertificate", func(t *testing.T) {
		opts := DefaultCertOptions()
		caCert, caKey, err := GenerateCA(opts)
		if err != nil {
			t.Fatalf("Failed to generate CA: %v", err)
		}

		certOpts := DefaultCertOptions()
		certOpts.CommonName = "node-1"
		certOpts.Hosts = []string{"localhost", "127.0.0.1", "node-1"}

		cert, key, err := GenerateCertificate(caCert, caKey, certOpts)
		if err != nil {
			t.Fatalf("Failed to generate certificate: %v", err)
		}

		if cert == nil {
			t.Fatal("Certificate is nil")
		}
		if key == nil {
			t.Fatal("Private key is nil")
		}

		if cert.IsCA {
			t.Error("Certificate should not be marked as CA")
		}

		// Verify DNS names
		found := false
		for _, name := range cert.DNSNames {
			if name == "node-1" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Certificate does not contain expected DNS name")
		}
	})

	// Test saving certificates
	t.Run("SaveCertificate", func(t *testing.T) {
		opts := DefaultCertOptions()
		caCert, _, err := GenerateCA(opts)
		if err != nil {
			t.Fatalf("Failed to generate CA: %v", err)
		}

		certFile := filepath.Join(tmpDir, "test.crt")
		if err := SaveCertificate(caCert, certFile); err != nil {
			t.Fatalf("Failed to save certificate: %v", err)
		}

		// Verify file exists
		if _, err := os.Stat(certFile); os.IsNotExist(err) {
			t.Error("Certificate file was not created")
		}
	})

	// Test saving private key
	t.Run("SavePrivateKey", func(t *testing.T) {
		opts := DefaultCertOptions()
		_, caKey, err := GenerateCA(opts)
		if err != nil {
			t.Fatalf("Failed to generate CA: %v", err)
		}

		keyFile := filepath.Join(tmpDir, "test.key")
		if err := SavePrivateKey(caKey, keyFile); err != nil {
			t.Fatalf("Failed to save private key: %v", err)
		}

		// Verify file exists
		if _, err := os.Stat(keyFile); os.IsNotExist(err) {
			t.Error("Private key file was not created")
		}

		// Note: File permissions on Windows work differently than Unix
		// The test just verifies the file was created successfully
	})

	// Test cluster certificate generation
	t.Run("GenerateClusterCertificates", func(t *testing.T) {
		outputDir := filepath.Join(tmpDir, "cluster-certs")
		nodeIDs := []string{"node-1", "node-2", "node-3"}
		hosts := map[string][]string{
			"node-1": {"127.0.0.1", "node-1.example.com"},
			"node-2": {"127.0.0.1", "node-2.example.com"},
			"node-3": {"127.0.0.1", "node-3.example.com"},
		}

		err := GenerateClusterCertificates(outputDir, nodeIDs, hosts)
		if err != nil {
			t.Fatalf("Failed to generate cluster certificates: %v", err)
		}

		// Verify CA files exist
		caFiles := []string{"ca.crt", "ca.key"}
		for _, file := range caFiles {
			path := filepath.Join(outputDir, file)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Errorf("CA file not created: %s", file)
			}
		}

		// Verify node certificates exist
		for _, nodeID := range nodeIDs {
			certFile := filepath.Join(outputDir, nodeID+".crt")
			keyFile := filepath.Join(outputDir, nodeID+".key")

			if _, err := os.Stat(certFile); os.IsNotExist(err) {
				t.Errorf("Certificate not created for node %s", nodeID)
			}
			if _, err := os.Stat(keyFile); os.IsNotExist(err) {
				t.Errorf("Private key not created for node %s", nodeID)
			}
		}
	})
}

func TestNetworkTransport(t *testing.T) {
	// Create temp directory for certificates
	tmpDir := t.TempDir()

	// Generate test certificates
	nodeIDs := []string{"node-1", "node-2"}
	hosts := map[string][]string{
		"node-1": {"localhost", "127.0.0.1"},
		"node-2": {"localhost", "127.0.0.1"},
	}

	err := GenerateClusterCertificates(tmpDir, nodeIDs, hosts)
	if err != nil {
		t.Fatalf("Failed to generate certificates: %v", err)
	}

	// Test plain TCP transport
	t.Run("PlainTransport", func(t *testing.T) {
		transport, err := NewNetworkTransport("127.0.0.1:17000", nil)
		if err != nil {
			t.Fatalf("Failed to create transport: %v", err)
		}
		defer transport.Shutdown()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := transport.Start(ctx); err != nil {
			t.Fatalf("Failed to start transport: %v", err)
		}

		if transport.Address() != "127.0.0.1:17000" {
			t.Errorf("Expected address 127.0.0.1:17000, got %s", transport.Address())
		}
	})

	// Test TLS transport
	t.Run("TLSTransport", func(t *testing.T) {
		tlsConfig := &TLSConfig{
			Enabled:    true,
			CertFile:   filepath.Join(tmpDir, "node-1.crt"),
			KeyFile:    filepath.Join(tmpDir, "node-1.key"),
			CAFile:     filepath.Join(tmpDir, "ca.crt"),
			VerifyPeer: true,
		}

		transport, err := NewNetworkTransport("127.0.0.1:17001", tlsConfig)
		if err != nil {
			t.Fatalf("Failed to create TLS transport: %v", err)
		}
		defer transport.Shutdown()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := transport.Start(ctx); err != nil {
			t.Fatalf("Failed to start TLS transport: %v", err)
		}

		if transport.Address() != "127.0.0.1:17001" {
			t.Errorf("Expected address 127.0.0.1:17001, got %s", transport.Address())
		}
	})

	// Test invalid TLS configuration
	t.Run("InvalidTLSConfig", func(t *testing.T) {
		tlsConfig := &TLSConfig{
			Enabled:  true,
			CertFile: "/nonexistent/cert.crt",
			KeyFile:  "/nonexistent/key.key",
		}

		_, err := NewNetworkTransport("127.0.0.1:17002", tlsConfig)
		if err == nil {
			t.Error("Expected error with invalid TLS config, got nil")
		}
	})
}

func TestBuildTLSConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate test certificates
	err := GenerateClusterCertificates(tmpDir, []string{"test-node"}, nil)
	if err != nil {
		t.Fatalf("Failed to generate certificates: %v", err)
	}

	t.Run("ValidConfig", func(t *testing.T) {
		cfg := &TLSConfig{
			Enabled:    true,
			CertFile:   filepath.Join(tmpDir, "test-node.crt"),
			KeyFile:    filepath.Join(tmpDir, "test-node.key"),
			CAFile:     filepath.Join(tmpDir, "ca.crt"),
			VerifyPeer: true,
		}

		tlsConfig, err := buildTLSConfig(cfg)
		if err != nil {
			t.Fatalf("Failed to build TLS config: %v", err)
		}

		if tlsConfig == nil {
			t.Fatal("TLS config is nil")
		}

		if len(tlsConfig.Certificates) == 0 {
			t.Error("No certificates in TLS config")
		}

		if tlsConfig.RootCAs == nil {
			t.Error("Root CAs not set")
		}

		if tlsConfig.ClientCAs == nil {
			t.Error("Client CAs not set")
		}
	})

	t.Run("MissingCertFile", func(t *testing.T) {
		cfg := &TLSConfig{
			Enabled:  true,
			CertFile: "/nonexistent/cert.crt",
			KeyFile:  filepath.Join(tmpDir, "test-node.key"),
		}

		_, err := buildTLSConfig(cfg)
		if err == nil {
			t.Error("Expected error with missing cert file")
		}
	})

	t.Run("MissingCAFile", func(t *testing.T) {
		cfg := &TLSConfig{
			Enabled:    true,
			CertFile:   filepath.Join(tmpDir, "test-node.crt"),
			KeyFile:    filepath.Join(tmpDir, "test-node.key"),
			CAFile:     "/nonexistent/ca.crt",
			VerifyPeer: true,
		}

		_, err := buildTLSConfig(cfg)
		if err == nil {
			t.Error("Expected error with missing CA file")
		}
	})

	t.Run("InsecureSkipVerify", func(t *testing.T) {
		cfg := &TLSConfig{
			Enabled:            true,
			CertFile:           filepath.Join(tmpDir, "test-node.crt"),
			KeyFile:            filepath.Join(tmpDir, "test-node.key"),
			InsecureSkipVerify: true,
		}

		tlsConfig, err := buildTLSConfig(cfg)
		if err != nil {
			t.Fatalf("Failed to build TLS config: %v", err)
		}

		if !tlsConfig.InsecureSkipVerify {
			t.Error("InsecureSkipVerify not set")
		}
	})
}
