package core

import (
	"context"
	"core/internal/types"
	observability "github.com/goletan/observability/pkg"
	resilience "github.com/goletan/resilience/pkg"
	resTypes "github.com/goletan/resilience/shared/types"
	services "github.com/goletan/services/pkg"
	serTypes "github.com/goletan/services/shared/types"
	"github.com/sony/gobreaker/v2"
	"go.uber.org/zap"
	"log"
)

type Core struct {
	Config        *types.CoreConfig
	Observability *observability.Observability
	Resilience    *resilience.DefaultResilienceService
	Services      *services.Services
}

// NewCore initializes the Core with essential components.
func NewCore(ctx context.Context) (*Core, error) {
	obs := initializeObservability()
	obs.Logger.Info("Core Initializing...")

	cfg, err := LoadCoreConfig(obs.Logger)
	if err != nil {
		return nil, err
	}

	res := resilience.NewResilienceService(
		"core",
		obs,
		func(err error) bool { return true }, // Retry on all errors
		&resTypes.CircuitBreakerCallbacks{
			OnOpen: func(name string, from, to gobreaker.State) {
				obs.Logger.Warn("Circuit breaker opened", zap.String("name", name))
			},
			OnClose: func(name string, from, to gobreaker.State) {
				obs.Logger.Info("Circuit breaker closed", zap.String("name", name))
			},
		},
	)

	newServices, err := services.NewServices(obs)
	if err != nil {
		obs.Logger.Error("Failed to initialize services", zap.Error(err))
		return nil, err
	}

	return &Core{
		Config:        cfg,
		Observability: obs,
		Resilience:    res,
		Services:      newServices,
	}, nil
}

// Start launches the Core's core components and begins service discovery.
func (c *Core) Start(ctx context.Context) error {
	c.Observability.Logger.Info("Starting initial service orchestration...")
	orchestrateServices(ctx, c)

	c.Observability.Logger.Info("Starting service discovery and event handling...")
	go c.startServiceWatcher(ctx)

	return nil
}

// Shutdown gracefully stops the Core's components.
func (c *Core) Shutdown(ctx context.Context) error {
	c.Observability.Logger.Info("Shutting down Services...")
	if err := c.Services.StopAll(ctx); err != nil {
		c.Observability.Logger.Error("Failed to stop services", zap.Error(err))
	}

	c.Observability.Logger.Info("Shutting down Resilience...")
	if err := c.Resilience.Shutdown(&ctx); err != nil {
		c.Observability.Logger.Error("Failed to shut down resilience", zap.Error(err))
		return err
	}

	c.Observability.Logger.Info("Core shut down successfully")
	return nil
}

// startServiceWatcher listens for service events and dynamically updates the service registry.
func (c *Core) startServiceWatcher(ctx context.Context) {
	eventCh, err := c.Services.Watch(ctx, "default-namespace")
	if err != nil {
		c.Observability.Logger.Fatal("Failed to start service watcher", zap.Error(err))
		return
	}

	for {
		select {
		case <-ctx.Done():
			c.Observability.Logger.Info("Stopping service watcher...")
			return
		case event, ok := <-eventCh:
			if !ok {
				c.Observability.Logger.Warn("Service watcher channel closed")
				return
			}

			switch event.Type {
			case "ADDED":
				c.handleServiceAdded(event.Service)
			case "DELETED":
				c.handleServiceDeleted(event.Service)
			case "MODIFIED":
				c.handleServiceModified(event.Service)
			}
		}
	}
}

// handleServiceAdded dynamically registers and initializes a new service.
func (c *Core) handleServiceAdded(endpoint serTypes.ServiceEndpoint) {
	c.Observability.Logger.Info("Adding service", zap.String("name", endpoint.Name), zap.String("address", endpoint.Address))
	service, err := c.Services.CreateService(endpoint)
	if err != nil {
		c.Observability.Logger.Error("Failed to create service", zap.String("name", endpoint.Name), zap.Error(err))
		return
	}

	if err := c.Services.Register(service); err != nil {
		c.Observability.Logger.Error("Failed to register service", zap.String("name", service.Name()), zap.Error(err))
		return
	}

	if err := service.Initialize(); err != nil {
		c.Observability.Logger.Error("Failed to initialize service", zap.String("name", service.Name()), zap.Error(err))
		return
	}

	if err := service.Start(); err != nil {
		c.Observability.Logger.Error("Failed to start service", zap.String("name", service.Name()), zap.Error(err))
	}
}

// handleServiceDeleted dynamically removes a service from the registry.
func (c *Core) handleServiceDeleted(endpoint serTypes.ServiceEndpoint) {
	c.Observability.Logger.Info("Removing service", zap.String("name", endpoint.Name), zap.String("address", endpoint.Address))
	// Implementation for stopping and unregistering services if needed
}

// handleServiceModified handles updates to an existing service.
func (c *Core) handleServiceModified(endpoint serTypes.ServiceEndpoint) {
	c.Observability.Logger.Info("Modifying service", zap.String("name", endpoint.Name), zap.String("address", endpoint.Address))
	// Implementation for updating services dynamically
}

// initializeObservability initializes the observability components (logger, metrics, tracing).
func initializeObservability() *observability.Observability {
	obs, err := observability.NewObserver()
	if err != nil {
		log.Fatal("Failed to initialize observability", err)
	}
	return obs
}
