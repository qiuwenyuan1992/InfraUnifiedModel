package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"github.com/spf13/viper"
)

type TopologyBrowseService interface {
	Node(context.Context, string, model.EntityIdentity) (*repository.TopologyNode, error)
	Neighbors(context.Context, string, model.EntityIdentity, repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error)
	Roots(context.Context, string, string, model.EntityType, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error)
	Children(context.Context, string, model.EntityIdentity, repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error)
	DeviceContext(context.Context, string, model.EntityIdentity) (*TopologyDeviceContext, error)
	RoomPowerContext(context.Context, string, model.EntityIdentity) (*TopologyRoomPowerContext, error)
	Scene(context.Context, string, model.EntityIdentity, repository.TopologyDirectoryQuery) (*TopologyScene, error)
}

type topologyBrowseService struct {
	repo repository.TopologyReadRepository
	auth *inventoryService
}

func NewTopologyBrowseService(repo repository.TopologyReadRepository, conf *viper.Viper) TopologyBrowseService {
	return &topologyBrowseService{repo: repo, auth: NewInventoryService(nil, conf).(*inventoryService)}
}

// ValidateTopologyIdentity 保留调用方提供的稳定身份，不从名称或属性推断 ID。
func ValidateTopologyIdentity(identity model.EntityIdentity) error {
	if !inventoryID(identity.SourceID) || len(identity.StableID) > 256 {
		return ErrInventoryInvalid
	}
	if _, err := identity.CanonicalJSON(); err != nil {
		return ErrInventoryInvalid
	}
	return nil
}

// NormalizeTopologyNeighborQuery 仅补齐查询默认值，不改变资产身份。
func NormalizeTopologyNeighborQuery(query repository.TopologyNeighborQuery) (repository.TopologyNeighborQuery, error) {
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.Direction == "" {
		query.Direction = "both"
	}
	if query.Limit < 1 || query.Limit > 200 || (query.Direction != "both" && query.Direction != "in" && query.Direction != "out") || (query.EdgeType != "" && !query.EdgeType.Valid()) {
		return query, ErrInventoryInvalid
	}
	return query, nil
}

func (s *topologyBrowseService) Node(ctx context.Context, user string, identity model.EntityIdentity) (*repository.TopologyNode, error) {
	if err := s.auth.authorize(user, "sources"); err != nil {
		return nil, err
	}
	if err := ValidateTopologyIdentity(identity); err != nil {
		return nil, err
	}
	if s.repo == nil {
		return nil, ErrInventoryNotReady
	}
	node, err := s.repo.Node(ctx, identity)
	return node, topologyBrowseError(err)
}

func (s *topologyBrowseService) Neighbors(ctx context.Context, user string, identity model.EntityIdentity, query repository.TopologyNeighborQuery) (*repository.TopologyNeighborhood, error) {
	if err := s.auth.authorize(user, "sources"); err != nil {
		return nil, err
	}
	if err := ValidateTopologyIdentity(identity); err != nil {
		return nil, err
	}
	query, err := NormalizeTopologyNeighborQuery(query)
	if err != nil {
		return nil, err
	}
	if s.repo == nil {
		return nil, ErrInventoryNotReady
	}
	neighborhood, err := s.repo.Neighbors(ctx, identity, query)
	return neighborhood, topologyBrowseError(err)
}

func topologyBrowseError(err error) error {
	if errors.Is(err, repository.ErrInventoryInvalid) {
		return ErrInventoryInvalid
	}
	return inventoryError(err)
}

type topologyDirectoryCursor struct {
	Version    int              `json:"v"`
	User       string           `json:"u"`
	Source     string           `json:"s"`
	EntityType model.EntityType `json:"e"`
	Parent     string           `json:"p"`
	After      string           `json:"a"`
}

const topologyDirectoryCursorDomain = "topology-directory-v1\n"

func NormalizeTopologyDirectoryQuery(q repository.TopologyDirectoryQuery) (repository.TopologyDirectoryQuery, error) {
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 100 || len(q.After) > 4096 {
		return q, ErrInventoryInvalid
	}
	return q, nil
}

func topologyDirectoryVID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *topologyBrowseService) directorySignature(payload string) []byte {
	mac := hmac.New(sha256.New, s.auth.cursorKey)
	_, _ = mac.Write([]byte(topologyDirectoryCursorDomain))
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func (s *topologyBrowseService) decodeDirectoryCursor(value string, binding topologyDirectoryCursor) (string, error) {
	if len(value) > 4096 {
		return "", ErrInventoryInvalid
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", ErrInventoryInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[1] || !hmac.Equal(signature, s.directorySignature(parts[0])) {
		return "", ErrInventoryInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != parts[0] {
		return "", ErrInventoryInvalid
	}
	var cursor topologyDirectoryCursor
	if json.Unmarshal(data, &cursor) != nil || !topologyDirectoryVID(cursor.After) {
		return "", ErrInventoryInvalid
	}
	after := cursor.After
	cursor.After = ""
	if cursor != binding {
		return "", ErrInventoryInvalid
	}
	return after, nil
}

func (s *topologyBrowseService) Roots(ctx context.Context, user, source string, entityType model.EntityType, query repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	if err := s.auth.authorize(user, "sources"); err != nil {
		return nil, err
	}
	if !inventoryID(source) || (entityType != model.EntityDataCenter && entityType != model.EntityPod) {
		return nil, ErrInventoryInvalid
	}
	return s.directory(ctx, user, model.EntityIdentity{SourceID: source, EntityType: entityType}, query)
}

func (s *topologyBrowseService) Children(ctx context.Context, user string, identity model.EntityIdentity, query repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	if err := s.auth.authorize(user, "sources"); err != nil {
		return nil, err
	}
	if err := ValidateTopologyIdentity(identity); err != nil {
		return nil, err
	}
	if identity.EntityType != model.EntityDataCenter && identity.EntityType != model.EntityRoom && identity.EntityType != model.EntityPod && identity.EntityType != model.EntityCabinet {
		return nil, ErrInventoryInvalid
	}
	return s.directory(ctx, user, identity, query)
}

func (s *topologyBrowseService) directory(ctx context.Context, user string, identity model.EntityIdentity, query repository.TopologyDirectoryQuery) (*repository.TopologyDirectoryPage, error) {
	query, err := NormalizeTopologyDirectoryQuery(query)
	if err != nil {
		return nil, err
	}
	if len(s.auth.cursorKey) < 32 || s.repo == nil {
		return nil, ErrInventoryNotReady
	}
	binding := topologyDirectoryCursor{Version: 1, User: user, Source: identity.SourceID, EntityType: identity.EntityType, Parent: identity.StableID}
	if query.After != "" {
		query.After, err = s.decodeDirectoryCursor(query.After, binding)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var page *repository.TopologyDirectoryPage
	if identity.StableID == "" {
		page, err = s.repo.Roots(ctx, identity.SourceID, identity.EntityType, query)
	} else {
		page, err = s.repo.Children(ctx, identity, query)
	}
	if err != nil {
		return nil, topologyReadFailure(err)
	}
	if page == nil {
		return nil, ErrInventoryNotReady
	}
	result := *page
	if result.Items == nil {
		result.Items = []repository.TopologyNode{}
	}
	if result.NextAfter != "" {
		if !topologyDirectoryVID(result.NextAfter) {
			return nil, ErrInventoryNotReady
		}
		binding.After = result.NextAfter
		data, _ := json.Marshal(binding)
		payload := base64.RawURLEncoding.EncodeToString(data)
		result.NextAfter = payload + "." + base64.RawURLEncoding.EncodeToString(s.directorySignature(payload))
	}
	return &result, nil
}

func topologyReadFailure(err error) error {
	if errors.Is(err, repository.ErrTopologyQueryBudget) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, repository.ErrInventoryNotFound) || errors.Is(err, repository.ErrInventoryInvalid) {
		return topologyBrowseError(err)
	}
	return ErrInventoryNotReady
}
