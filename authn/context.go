package auth

import (
	"encoding/json"
)

// TraderContext represents the context of a trader in the database.
// This model persists trader information and associated metadata.
type TraderContext struct {
	TraderID      string          `gorm:"type:varchar(100);column:trader_id;primaryKey;not null" json:"trader_id"`
	TraderContext json.RawMessage `gorm:"type:jsonb;column:trader_context;serializer:json;not null" json:"trader_context"`
}

// TableName specifies the database table name for TraderContext
func (t *TraderContext) TableName() string {
	return "trader_contexts"
}

// AuthContext represents the authentication context available in a request.
// This is a transient context that is injected into the request by the auth middleware.
// It contains trader information retrieved from the database based on the token.
// Also includes OUHandle from the token claims for potential authorization decisions.
//
// Future: We can extend this struct to include more fields from the token or database as needed.
type AuthContext struct {
	*TraderContext
	OUHandle string `json:"ouHandle"`
}

// GetTraderID is a convenience method to get the trader ID directly from AuthContext.
// Returns empty string if AuthContext is nil.
func (a *AuthContext) GetTraderID() string {
	if a == nil || a.TraderContext == nil {
		return ""
	}
	return a.TraderID
}

// GetTraderContextMap returns the trader context as a map for convenient access.
// If no context exists, it returns an empty map.
func (ac *AuthContext) GetTraderContextMap() (map[string]any, error) {
	contextMap := make(map[string]any)
	if ac == nil || ac.TraderContext == nil || len(ac.TraderContext.TraderContext) == 0 {
		return contextMap, nil
	}
	err := json.Unmarshal(ac.TraderContext.TraderContext, &contextMap)
	if err != nil {
		return nil, err
	}
	return contextMap, nil
}

// GetOUHandle is a convenience method to get the OUHandle directly from AuthContext.
// Returns empty string if AuthContext is nil or OUHandle is not set.
func (a *AuthContext) GetOUHandle() string {
	if a == nil {
		return ""
	}
	return a.OUHandle
}
