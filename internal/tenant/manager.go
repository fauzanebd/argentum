package tenant

import (
	"context"
	"sync"
	"time"
)

// Manager handles multi-tenant isolation and configuration
type Manager struct {
	tenants map[string]*Tenant
	mu      sync.RWMutex
}

// Tenant represents a business/tenant configuration
type Tenant struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	SchemaName string                 `json:"schema_name"`
	DatabaseID int                    `json:"database_id"`
	Config     map[string]interface{} `json:"config"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
	IsActive   bool                   `json:"is_active"`
}

// TenantConfig contains LLM and other configurations per tenant
type TenantConfig struct {
	LLMProvider string `json:"llm_provider"`
	LLMModel    string `json:"llm_model"`
	LLMAPIKey   string `json:"llm_api_key"`
}

// NewManager creates a new tenant manager
func NewManager() *Manager {
	return &Manager{
		tenants: make(map[string]*Tenant),
	}
}

// GetTenant retrieves a tenant by ID
func (m *Manager) GetTenant(tenantID string) (*Tenant, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenant, exists := m.tenants[tenantID]
	return tenant, exists
}

// RegisterTenant registers a new tenant
func (m *Manager) RegisterTenant(tenant *Tenant) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant.CreatedAt = time.Now()
	tenant.UpdatedAt = time.Now()
	tenant.IsActive = true

	m.tenants[tenant.ID] = tenant
}

// UpdateTenant updates an existing tenant
func (m *Manager) UpdateTenant(tenantID string, updates map[string]interface{}) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, exists := m.tenants[tenantID]
	if !exists {
		return false
	}

	if name, ok := updates["name"].(string); ok {
		tenant.Name = name
	}
	if schema, ok := updates["schema_name"].(string); ok {
		tenant.SchemaName = schema
	}
	if config, ok := updates["config"].(map[string]interface{}); ok {
		tenant.Config = config
	}

	tenant.UpdatedAt = time.Now()
	return true
}

// DeactivateTenant deactivates a tenant
func (m *Manager) DeactivateTenant(tenantID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, exists := m.tenants[tenantID]
	if !exists {
		return false
	}

	tenant.IsActive = false
	tenant.UpdatedAt = time.Now()
	return true
}

// ListTenants returns all registered tenants
func (m *Manager) ListTenants() []*Tenant {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenants := make([]*Tenant, 0, len(m.tenants))
	for _, t := range m.tenants {
		tenants = append(tenants, t)
	}
	return tenants
}

// GetTenantConfig retrieves tenant-specific configuration
func (m *Manager) GetTenantConfig(tenantID string) *TenantConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenant, exists := m.tenants[tenantID]
	if !exists {
		return nil
	}

	config := &TenantConfig{}
	if cfg, ok := tenant.Config["llm"].(map[string]interface{}); ok {
		if provider, ok := cfg["provider"].(string); ok {
			config.LLMProvider = provider
		}
		if model, ok := cfg["model"].(string); ok {
			config.LLMModel = model
		}
		if key, ok := cfg["api_key"].(string); ok {
			config.LLMAPIKey = key
		}
	}

	return config
}

// Middleware extracts tenant ID from context
func ExtractTenantID(ctx context.Context) string {
	if tenantID, ok := ctx.Value("tenant_id").(string); ok {
		return tenantID
	}
	return "default"
}

// WithTenantID adds tenant ID to context
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, "tenant_id", tenantID)
}
