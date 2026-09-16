package repository

import (
	"net"

	"UnifiedInfraTopology/internal/model"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
)

func (r graphRow) text(key string) (string, error) {
	v := r[key]
	if v == nil || v.SVal == nil {
		return "", graphMalformed()
	}
	return string(v.SVal), nil
}

func (r graphRow) integer(key string) (int64, error) {
	v := r[key]
	if v == nil || v.IVal == nil {
		return 0, graphMalformed()
	}
	return *v.IVal, nil
}

func (r graphRow) nullableText(key string) (*string, error) {
	v := r[key]
	if v != nil && v.NVal != nil && *v.NVal == nebula.NullType___NULL__ {
		return nil, nil
	}
	s, err := r.text(key)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r graphRow) nullableInteger(key string) (*int64, error) {
	v := r[key]
	if v != nil && v.NVal != nil && *v.NVal == nebula.NullType___NULL__ {
		return nil, nil
	}
	n, err := r.integer(key)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (r graphRow) identity(scope, kind, id string) error {
	actualScope, err := r.text("scope_id")
	if err != nil || actualScope != scope {
		return graphMalformed()
	}
	actualID, err := r.text("entity_id")
	if err != nil || !graphEntityID.MatchString(actualID) || actualID != id {
		return graphMalformed()
	}
	vid, err := r.text("vid")
	if err != nil || vid != graphVID(scope, kind, id) || len(vid) != 67 {
		return graphMalformed()
	}
	return nil
}

func (r graphRow) strings(fields map[string]*string) error {
	for key, target := range fields {
		value, err := r.text(key)
		if err != nil {
			return err
		}
		*target = value
	}
	return nil
}

func (r graphRow) device() (model.Device, error) {
	v := model.Device{}
	if err := r.strings(map[string]*string{"entity_id": &v.EntityID, "name": &v.Name, "device_kind": &v.DeviceKind, "role": &v.Role, "lifecycle": &v.Lifecycle, "resolution_status": &v.ResolutionStatus}); err != nil {
		return v, err
	}
	serial, err := r.nullableText("serial_number")
	v.SerialNumber = serial
	return v, err
}

func (r graphRow) iface() (model.Interface, error) {
	v := model.Interface{}
	if err := r.strings(map[string]*string{"entity_id": &v.EntityID, "device_id": &v.DeviceID, "namespace": &v.Namespace, "source_name": &v.SourceName, "normalized_name": &v.NormalizedName, "interface_kind": &v.InterfaceKind, "admin_state": &v.AdminState, "oper_state": &v.OperState, "lifecycle": &v.Lifecycle, "resolution_status": &v.ResolutionStatus}); err != nil {
		return v, err
	}
	if !graphEntityID.MatchString(v.DeviceID) {
		return v, graphMalformed()
	}
	speed, err := r.nullableInteger("speed_bps")
	if err != nil {
		return v, err
	}
	if speed != nil && *speed < 0 {
		return v, graphMalformed()
	}
	v.SpeedBPS = speed
	return v, nil
}

func (r graphRow) address() (model.Address, error) {
	v := model.Address{}
	if err := r.strings(map[string]*string{"entity_id": &v.EntityID, "device_id": &v.DeviceID, "address_scope_key": &v.AddressScopeKey, "scope_status": &v.ScopeStatus, "purpose": &v.Purpose, "lifecycle": &v.Lifecycle, "resolution_status": &v.ResolutionStatus}); err != nil {
		return v, err
	}
	if !graphEntityID.MatchString(v.DeviceID) {
		return v, graphMalformed()
	}
	iface, err := r.nullableText("interface_id")
	if err != nil {
		return v, err
	}
	if iface != nil && !graphEntityID.MatchString(*iface) {
		return v, graphMalformed()
	}
	v.InterfaceID = iface
	family, err := r.integer("address_family")
	if err != nil || (family != 4 && family != 6) {
		return v, graphMalformed()
	}
	v.AddressFamily = int(family)
	text, err := r.text("address")
	if err != nil {
		return v, err
	}
	ip := net.ParseIP(text)
	if ip == nil || (family == 4 && ip.To4() == nil) || (family == 6 && ip.To4() != nil) {
		return v, graphMalformed()
	}
	// DTO 使用 16 字节 IP；IPv4 保留 IPv4-mapped IPv6 形式。
	v.Address = append([]byte(nil), ip.To16()...)
	prefix, err := r.nullableInteger("prefix_length")
	if err != nil {
		return v, err
	}
	if prefix != nil {
		max := int64(128)
		if family == 4 {
			max = 32
		}
		if *prefix < 0 || *prefix > max {
			return v, graphMalformed()
		}
		n := int(*prefix)
		v.PrefixLength = &n
	}
	return v, nil
}
