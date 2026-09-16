package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

type inventoryCursor struct {
	Version         int        `json:"v"`
	UserID          string     `json:"u"`
	Resource        string     `json:"r"`
	ScopeID         string     `json:"s"`
	ParentID        string     `json:"p"`
	GenerationID    string     `json:"g"`
	ProjectionEpoch int64      `json:"e"`
	Filters         string     `json:"f"`
	LastID          string     `json:"i"`
	LastCreatedAt   *time.Time `json:"t,omitempty"`
}

func inventoryID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func inventoryToken(v string) bool {
	if len(v) > 64 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func inventoryOneOf(value string, allowed ...string) bool {
	if value == "" {
		return true
	}
	for _, v := range allowed {
		if value == v {
			return true
		}
	}
	return false
}

func validateInventoryQuery(resource, parentID string, q InventoryQuery) error {
	if q.Limit < 0 || q.Limit > 200 || len(q.Cursor) > 4096 {
		return ErrInventoryInvalid
	}
	if q.GenerationID != "" && !inventoryID(q.GenerationID) {
		return ErrInventoryInvalid
	}
	if q.SourceID != "" && !inventoryID(q.SourceID) {
		return ErrInventoryInvalid
	}
	if !inventoryToken(q.DeviceKind) || !inventoryOneOf(q.Lifecycle, "active", "retired") || !inventoryOneOf(q.InterfaceKind, "physical", "logical", "aggregation", "unknown") || !inventoryOneOf(q.AddressFamily, "4", "6") || !inventoryOneOf(q.Status, "queued", "running", "validating", "publishing", "succeeded", "failed", "canceled") {
		return ErrInventoryInvalid
	}
	if len(q.Name) > 255 || !utf8.ValidString(q.Name) || strings.ContainsAny(q.Name, "\x00\r\n") {
		return ErrInventoryInvalid
	}
	isVersion := resource == "devices" || resource == "interfaces" || resource == "addresses"
	if !isVersion && q.GenerationID != "" {
		return ErrInventoryInvalid
	}
	if resource != "devices" && (q.DeviceKind != "" || q.Name != "" || q.Lifecycle != "") {
		return ErrInventoryInvalid
	}
	if resource != "interfaces" && q.InterfaceKind != "" {
		return ErrInventoryInvalid
	}
	if resource != "addresses" && q.AddressFamily != "" {
		return ErrInventoryInvalid
	}
	if resource != "sync-runs" && (q.Status != "" || q.SourceID != "") {
		return ErrInventoryInvalid
	}
	if resource == "interfaces" || resource == "addresses" {
		if !inventoryID(parentID) {
			return ErrInventoryInvalid
		}
	} else if parentID != "" {
		return ErrInventoryInvalid
	}
	switch resource {
	case "scopes", "devices", "interfaces", "addresses", "sources", "generations", "sync-runs":
		return nil
	default:
		return ErrInventoryInvalid
	}
}

func inventoryFilterHash(q InventoryQuery) string {
	q.Cursor = ""
	q.GenerationID = ""
	q.Limit = 0
	data, _ := json.Marshal(q)
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
	if err = json.Unmarshal(data, &cursor); err != nil || cursor.Version != 2 || !inventoryID(cursor.LastID) {
		return cursor, ErrInventoryInvalid
	}
	return cursor, nil
}
