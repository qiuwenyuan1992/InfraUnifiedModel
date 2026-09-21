package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

type EntityIdentity struct {
	SourceID   string
	EntityType EntityType
	StableID   string
}

func (i EntityIdentity) validate() error {
	if i.SourceID == "" {
		return errors.New("source ID is required")
	}
	if !i.EntityType.Valid() {
		return fmt.Errorf("invalid entity type %q", i.EntityType)
	}
	if i.StableID == "" {
		return errors.New("stable ID is required")
	}
	return nil
}

func (i EntityIdentity) CanonicalJSON() ([]byte, error) {
	if err := i.validate(); err != nil {
		return nil, err
	}
	return json.Marshal([]string{i.SourceID, string(i.EntityType), i.StableID})
}

func (i EntityIdentity) VID() (string, error) {
	if err := i.validate(); err != nil {
		return "", err
	}
	return hashJSON([]string{"vertex", i.SourceID, string(i.EntityType), i.StableID})
}

type RelationIdentity struct {
	SourceID         string
	EdgeType         EdgeType
	Kind             RelationKind
	From             EntityIdentity
	To               EntityIdentity
	SourceRelationID string
	Discriminator    string
}

func (i RelationIdentity) validate() error {
	if i.SourceID == "" {
		return errors.New("source ID is required")
	}
	if !i.EdgeType.Valid() {
		return fmt.Errorf("invalid edge type %q", i.EdgeType)
	}
	if !i.Kind.Valid() {
		return fmt.Errorf("invalid relation kind %q", i.Kind)
	}
	if err := i.From.validate(); err != nil {
		return fmt.Errorf("invalid from identity: %w", err)
	}
	if err := i.To.validate(); err != nil {
		return fmt.Errorf("invalid to identity: %w", err)
	}
	if i.From.SourceID != i.SourceID || i.To.SourceID != i.SourceID {
		return errors.New("relation and endpoint sources must match")
	}
	return nil
}

func (i RelationIdentity) ID() (string, error) {
	if err := i.validate(); err != nil {
		return "", err
	}
	if i.SourceRelationID != "" {
		return hashJSON([]string{
			"relation",
			i.SourceID,
			string(i.EdgeType),
			string(i.Kind),
			i.SourceRelationID,
		})
	}

	fromJSON, err := i.From.CanonicalJSON()
	if err != nil {
		return "", err
	}
	toJSON, err := i.To.CanonicalJSON()
	if err != nil {
		return "", err
	}
	return hashJSON([]string{
		"relation",
		i.SourceID,
		string(i.EdgeType),
		string(i.Kind),
		string(fromJSON),
		string(toJSON),
		i.Discriminator,
	})
}

func (i RelationIdentity) Rank() (int64, error) {
	relationID, err := i.ID()
	if err != nil {
		return 0, err
	}
	return rankFromRelationID(relationID)
}

func hashJSON(parts []string) (string, error) {
	canonical, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func rankFromRelationID(relationID string) (int64, error) {
	if len(relationID) != sha256.Size*2 {
		return 0, errors.New("relation ID must be 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(relationID); err != nil {
		return 0, fmt.Errorf("invalid relation ID: %w", err)
	}
	value, err := strconv.ParseUint(relationID[:16], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse relation rank: %w", err)
	}
	value &= math.MaxInt64
	if value == 0 {
		value = 1
	}
	return int64(value), nil
}
