package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type inventoryCursor struct {
	Version       int        `json:"v"`
	UserID        string     `json:"u"`
	Resource      string     `json:"r"`
	Filters       string     `json:"f"`
	LastID        string     `json:"i"`
	LastCreatedAt *time.Time `json:"t,omitempty"`
}

func inventoryID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, current := range id {
		if !(current >= '0' && current <= '9' || current >= 'a' && current <= 'f') {
			return false
		}
	}
	return true
}

func inventoryOneOf(value string, allowed ...string) bool {
	if value == "" {
		return true
	}
	for _, current := range allowed {
		if value == current {
			return true
		}
	}
	return false
}

func validateInventoryQuery(resource, parentID string, query InventoryQuery) error {
	if parentID != "" || query.Limit < 0 || query.Limit > 200 || len(query.Cursor) > 4096 {
		return ErrInventoryInvalid
	}
	if query.SourceID != "" && !inventoryID(query.SourceID) {
		return ErrInventoryInvalid
	}
	if !inventoryOneOf(query.Status, "queued", "running", "validating", "publishing", "succeeded", "failed", "canceled") {
		return ErrInventoryInvalid
	}
	switch resource {
	case "sources":
		if query.Status != "" || query.SourceID != "" {
			return ErrInventoryInvalid
		}
	case "sync-runs":
	default:
		return ErrInventoryInvalid
	}
	return nil
}

func inventoryFilterHash(query InventoryQuery) string {
	query.Cursor = ""
	query.Limit = 0
	data, _ := json.Marshal(query)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *inventoryService) encodeCursor(cursor inventoryCursor) string {
	data, _ := json.Marshal(cursor)
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, s.cursorKey)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *inventoryService) decodeCursor(value string) (inventoryCursor, error) {
	var cursor inventoryCursor
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursor, ErrInventoryInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[1] {
		return cursor, ErrInventoryInvalid
	}
	mac := hmac.New(sha256.New, s.cursorKey)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor, ErrInventoryInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor, ErrInventoryInvalid
	}
	if err = json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || !inventoryID(cursor.LastID) {
		return cursor, ErrInventoryInvalid
	}
	return cursor, nil
}
