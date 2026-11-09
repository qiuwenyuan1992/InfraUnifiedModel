package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	v1 "UnifiedInfraTopology/api/v1"
	"UnifiedInfraTopology/internal/model"
	"UnifiedInfraTopology/internal/repository"
	"UnifiedInfraTopology/internal/service"
	"github.com/gin-gonic/gin"
)

type TopologyBrowseHandler interface {
	Node(*gin.Context)
	Neighbors(*gin.Context)
	Roots(*gin.Context)
	Children(*gin.Context)
	DeviceContext(*gin.Context)
}
type topologyBrowseHandler struct {
	*Handler
	service service.TopologyBrowseService
}

func NewTopologyBrowseHandler(h *Handler, s service.TopologyBrowseService) TopologyBrowseHandler {
	return &topologyBrowseHandler{Handler: h, service: s}
}

func topologyIdentity(query url.Values) (model.EntityIdentity, error) {
	identity := model.EntityIdentity{SourceID: query.Get("source_id"), EntityType: model.EntityType(query.Get("entity_type")), StableID: query.Get("stable_id")}
	return identity, service.ValidateTopologyIdentity(identity)
}

func topologyError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrInventoryNotReady) {
		c.JSON(http.StatusServiceUnavailable, v1.Response{Code: 50301, Message: "topology_not_ready", Data: gin.H{}})
		return
	}
	inventoryError(c, err)
}

// Node godoc
// @Summary Locate a topology node by its exact stable identity
// @Tags topology
// @Security Bearer
// @Produce json
// @Param source_id query string true "32 lowercase hexadecimal source ID"
// @Param entity_type query string true "Entity type" Enums(device,interface,gpu,pod,cabinet,data_center,rpp,ups_group,ups,transformer)
// @Param stable_id query string true "Stable ID, up to 256 bytes; power entities require canonical positive int64 inst_id"
// @Success 200 {object} v1.Response{data=repository.TopologyNode}
// @Failure 400,401,403,404,500,503,504 {object} v1.Response
// @Router /inventory/topology/node [get]
func (h *topologyBrowseHandler) Node(c *gin.Context) {
	query, err := inventoryQuery(c, "source_id", "entity_type", "stable_id")
	if err != nil {
		topologyError(c, err)
		return
	}
	identity, err := topologyIdentity(query)
	if err != nil {
		topologyError(c, err)
		return
	}
	node, err := h.service.Node(c.Request.Context(), GetUserIdFromCtx(c), identity)
	if err != nil {
		topologyError(c, err)
		return
	}
	inventorySuccess(c, http.StatusOK, node)
}

// Neighbors godoc
// @Summary Read a bounded, directed one-hop topology neighborhood
// @Tags topology
// @Security Bearer
// @Produce json
// @Param source_id query string true "32 lowercase hexadecimal source ID"
// @Param entity_type query string true "Entity type" Enums(device,interface,gpu,pod,cabinet,data_center,rpp,ups_group,ups,transformer)
// @Param stable_id query string true "Stable ID, up to 256 bytes; power entities require canonical positive int64 inst_id"
// @Param limit query int false "Maximum returned edges" minimum(1) maximum(200) default(100)
// @Param direction query string false "Stored edge direction" Enums(in,out,both) default(both)
// @Param edge_type query string false "Optional edge family" Enums(spatial_relation,composition_relation,network_relation,power_relation)
// @Success 200 {object} v1.Response{data=repository.TopologyNeighborhood}
// @Failure 400,401,403,404,500,503,504 {object} v1.Response
// @Router /inventory/topology/neighbors [get]
func (h *topologyBrowseHandler) Neighbors(c *gin.Context) {
	query, err := inventoryQuery(c, "source_id", "entity_type", "stable_id", "limit", "direction", "edge_type")
	if err != nil {
		topologyError(c, err)
		return
	}
	identity, err := topologyIdentity(query)
	if err != nil {
		topologyError(c, err)
		return
	}
	options := repository.TopologyNeighborQuery{Direction: query.Get("direction"), EdgeType: model.EdgeType(query.Get("edge_type"))}
	if query.Has("limit") {
		options.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || options.Limit < 1 {
			topologyError(c, service.ErrInventoryInvalid)
			return
		}
	}
	options, err = service.NormalizeTopologyNeighborQuery(options)
	if err != nil {
		topologyError(c, err)
		return
	}
	neighborhood, err := h.service.Neighbors(c.Request.Context(), GetUserIdFromCtx(c), identity, options)
	if err != nil {
		topologyError(c, err)
		return
	}
	inventorySuccess(c, http.StatusOK, neighborhood)
}

func topologyDirectoryQuery(query url.Values) (repository.TopologyDirectoryQuery, error) {
	options := repository.TopologyDirectoryQuery{After: query.Get("after")}
	if query.Has("limit") {
		limit, err := strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 {
			return options, service.ErrInventoryInvalid
		}
		options.Limit = limit
	}
	return service.NormalizeTopologyDirectoryQuery(options)
}

// Roots godoc
// @Summary List data center or POD topology roots with an opaque cursor
// @Tags topology
// @Security Bearer
// @Produce json
// @Param source_id query string true "32 lowercase hexadecimal source ID"
// @Param entity_type query string true "Directory root type" Enums(data_center,pod)
// @Param limit query int false "Maximum returned nodes" minimum(1) maximum(100) default(50)
// @Param after query string false "Opaque signed directory cursor, up to 4096 bytes"
// @Success 200 {object} v1.Response{data=repository.TopologyDirectoryPage}
// @Failure 400,401,403,404,500,503,504 {object} v1.Response
// @Router /inventory/topology/roots [get]
func (h *topologyBrowseHandler) Roots(c *gin.Context) {
	query, err := inventoryQuery(c, "source_id", "entity_type", "limit", "after")
	if err != nil {
		topologyError(c, err)
		return
	}
	kind := model.EntityType(query.Get("entity_type"))
	identity := model.EntityIdentity{SourceID: query.Get("source_id"), EntityType: kind, StableID: "directory"}
	if service.ValidateTopologyIdentity(identity) != nil || (kind != model.EntityDataCenter && kind != model.EntityPod) {
		topologyError(c, service.ErrInventoryInvalid)
		return
	}
	options, err := topologyDirectoryQuery(query)
	if err != nil {
		topologyError(c, err)
		return
	}
	page, err := h.service.Roots(c.Request.Context(), GetUserIdFromCtx(c), identity.SourceID, kind, options)
	if err != nil {
		topologyError(c, err)
		return
	}
	inventorySuccess(c, http.StatusOK, page)
}

// Children godoc
// @Summary List explicit directory children with a parent-bound opaque cursor
// @Tags topology
// @Security Bearer
// @Produce json
// @Param source_id query string true "32 lowercase hexadecimal source ID"
// @Param entity_type query string true "Parent directory type" Enums(data_center,pod,cabinet)
// @Param stable_id query string true "Exact stable parent ID, up to 256 bytes"
// @Param limit query int false "Maximum returned nodes" minimum(1) maximum(100) default(50)
// @Param after query string false "Opaque signed directory cursor, up to 4096 bytes"
// @Success 200 {object} v1.Response{data=repository.TopologyDirectoryPage}
// @Failure 400,401,403,404,500,503,504 {object} v1.Response
// @Router /inventory/topology/children [get]
func (h *topologyBrowseHandler) Children(c *gin.Context) {
	query, err := inventoryQuery(c, "source_id", "entity_type", "stable_id", "limit", "after")
	if err != nil {
		topologyError(c, err)
		return
	}
	identity, err := topologyIdentity(query)
	if err != nil {
		topologyError(c, err)
		return
	}
	if identity.EntityType != model.EntityDataCenter && identity.EntityType != model.EntityPod && identity.EntityType != model.EntityCabinet {
		topologyError(c, service.ErrInventoryInvalid)
		return
	}
	options, err := topologyDirectoryQuery(query)
	if err != nil {
		topologyError(c, err)
		return
	}
	page, err := h.service.Children(c.Request.Context(), GetUserIdFromCtx(c), identity, options)
	if err != nil {
		topologyError(c, err)
		return
	}
	inventorySuccess(c, http.StatusOK, page)
}

// DeviceContext godoc
// @Summary Read fixed device dependencies without recursively expanding remote devices or power leaves
// @Tags topology
// @Security Bearer
// @Produce json
// @Param source_id query string true "32 lowercase hexadecimal source ID"
// @Param stable_id query string true "Exact device stable ID, up to 256 bytes"
// @Success 200 {object} v1.Response{data=service.TopologyDeviceContext}
// @Failure 400,401,403,404,500,503,504 {object} v1.Response
// @Router /inventory/topology/device-context [get]
func (h *topologyBrowseHandler) DeviceContext(c *gin.Context) {
	query, err := inventoryQuery(c, "source_id", "stable_id")
	if err != nil {
		topologyError(c, err)
		return
	}
	identity := model.EntityIdentity{SourceID: query.Get("source_id"), EntityType: model.EntityDevice, StableID: query.Get("stable_id")}
	if err := service.ValidateTopologyIdentity(identity); err != nil {
		topologyError(c, err)
		return
	}
	result, err := h.service.DeviceContext(c.Request.Context(), GetUserIdFromCtx(c), identity)
	if err != nil {
		topologyError(c, err)
		return
	}
	inventorySuccess(c, http.StatusOK, result)
}
