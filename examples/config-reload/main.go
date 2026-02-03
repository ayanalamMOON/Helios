package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/helios/helios/internal/api"
	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/atlas/aof"
	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/auth/rbac"
	"github.com/helios/helios/internal/config"
	"github.com/helios/helios/internal/observability"
	"github.com/helios/helios/internal/queue"
	"github.com/helios/helios/internal/rate"
)

// Example demonstrating configuration reloading integration
func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	// Create configuration manager
	configManager, err := config.NewManager(configPath)
	if err != nil {
		log.Fatalf("Failed to create config manager: %v", err)
	}

	cfg := configManager.GetConfig()
	log.Printf("Loaded configuration from %s", configPath)
	log.Printf("Node ID: %s", cfg.Immutable.NodeID)
	log.Printf("Log Level: %s", cfg.Observability.LogLevel)

	// Create logger
	logger := observability.NewLogger("main")
	logger.SetLevel(observability.LogLevel(cfg.Observability.LogLevel))

	// Create Atlas instance
	atlasConfig := &atlas.Config{
		DataDir:          cfg.Immutable.DataDir,
		AOFSyncMode:      getAOFSyncMode(cfg.Performance.AOFSyncMode),
		SnapshotInterval: cfg.Performance.SnapshotInterval,
		MinCommands:      1000,
	}
	atlasInstance, err := atlas.New(atlasConfig)
	if err != nil {
		log.Fatalf("Failed to create Atlas: %v", err)
	}
	defer atlasInstance.Close()

	// Create services
	authService := auth.NewService(atlasInstance)
	rbacService := rbac.NewService(atlasInstance)
	rateLimiter := rate.NewLimiter(atlasInstance)
	queueInstance := queue.NewQueue(atlasInstance, queue.DefaultConfig())

	// Initialize RBAC roles
	if err := rbacService.InitializeDefaultRoles(); err != nil {
		log.Fatalf("Failed to initialize RBAC: %v", err)
	}

	// Create rate limiter config from our config
	rateConfig := rate.DefaultConfig()

	// Create gateway
	gateway := api.NewGateway(
		authService,
		rbacService,
		rateLimiter,
		queueInstance,
		rateConfig,
	)

	// Set configuration manager on gateway
	gateway.SetConfigManager(configManager)

	// Start automatic file watching for configuration changes
	logger.Info("Starting automatic configuration file watching", nil)
	if err := configManager.StartWatching(); err != nil {
		log.Fatalf("Failed to start config file watching: %v", err)
	}
	defer configManager.StopWatching()

	// Register reload listeners to apply configuration changes
	configManager.AddReloadListener(func(old, new *config.Config) error {
		logger.Info("Configuration reload triggered", map[string]interface{}{
			"old_log_level": old.Observability.LogLevel,
			"new_log_level": new.Observability.LogLevel,
			"trigger":       "file_watcher",
		})

		// Update log level
		if old.Observability.LogLevel != new.Observability.LogLevel {
			logger.Info("Updating log level", map[string]interface{}{
				"from": old.Observability.LogLevel,
				"to":   new.Observability.LogLevel,
			})
			logger.SetLevel(observability.LogLevel(new.Observability.LogLevel))
		}

		// Update rate limiter
		if old.RateLimiting != new.RateLimiting {
			logger.Info("Updating rate limiter", map[string]interface{}{
				"old_rps": old.RateLimiting.RequestsPerSecond,
				"new_rps": new.RateLimiting.RequestsPerSecond,
			})
			// In production, you would update the rate limiter here
			// rateLimiter.UpdateConfig(...)
		}

		// Validate performance settings
		if new.Performance.MaxConnections < 100 {
			return fmt.Errorf("max_connections too low: %d (minimum 100)",
				new.Performance.MaxConnections)
		}

		return nil
	})

	// Register routes
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)

	// Start HTTP server
	server := &http.Server{
		Addr:    cfg.Immutable.ListenAddr,
		Handler: mux,
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range sigChan {
			if sig == syscall.SIGHUP {
				// Manual reload on SIGHUP (file watching already handles automatic reloads)
				logger.Info("Received SIGHUP, manually triggering configuration reload", nil)
				if err := configManager.Reload(); err != nil {
					logger.Error("Failed to reload configuration", map[string]interface{}{
						"error": err.Error(),
					})
				} else {
					logger.Info("Configuration reloaded successfully")
					count, lastReload := configManager.GetReloadStats()
					logger.Info("Reload statistics", map[string]interface{}{
						"reload_count": count,
						"last_reload":  lastReload,
					})
				}
			} else {
				// Shutdown on SIGINT/SIGTERM
				logger.Info("Shutting down gracefully...")
				server.Close()
				return
			}
		}
	}()

	logger.Info("Server starting", map[string]interface{}{
		"addr":      cfg.Immutable.ListenAddr,
		"log_level": cfg.Observability.LogLevel,
	})

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Fatal("Server error", map[string]interface{}{
			"error": err.Error(),
		})
	}

	logger.Info("Server stopped")
}

func getAOFSyncMode(mode string) aof.SyncMode {
	switch mode {
	case "always":
		return aof.SyncEvery
	case "every":
		return aof.SyncInterval
	case "no":
		return aof.SyncNone
	default:
		return aof.SyncInterval // Default to interval
	}
}
